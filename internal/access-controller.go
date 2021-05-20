package accesscontroller

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/buraksezer/consistent"
	"github.com/hashicorp/memberlist"

	aclpb "github.com/authorizer-tech/access-controller/gen/go/authorizer-tech/accesscontroller/v1alpha1"
	log "github.com/sirupsen/logrus"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

var peerNamespaceConfigs PeerNamespaceConfigStore = &inmemPeerNamespaceConfigStore{
	configs: make(map[string]map[string]map[time.Time]*aclpb.NamespaceConfig),
}

type AccessController struct {
	aclpb.UnimplementedCheckServiceServer
	aclpb.UnimplementedWriteServiceServer
	aclpb.UnimplementedReadServiceServer
	aclpb.UnimplementedExpandServiceServer
	aclpb.UnimplementedNamespaceConfigServiceServer

	*Node
	RelationTupleStore
	NamespaceManager
	NodeConfigs
}

type AccessControllerOption func(*AccessController)

// WithStore sets the AccessController's RelationTupleStore.
func WithStore(store RelationTupleStore) AccessControllerOption {
	return func(ac *AccessController) {
		ac.RelationTupleStore = store
	}
}

// WithNamespaceManager sets the AccessController's NamespaceManager.
func WithNamespaceManager(m NamespaceManager) AccessControllerOption {
	return func(ac *AccessController) {
		ac.NamespaceManager = m
	}
}

// WithNodeConfigs sets the AccessController's NodeConfigs.
func WithNodeConfigs(cfg NodeConfigs) AccessControllerOption {
	return func(ac *AccessController) {
		ac.NodeConfigs = cfg
	}
}

// watchNamespaceConfigs is a background process that continuously monitors changes to
// namespace configurations. When changes happen, these config changes are made local
// to this node in an in-memory namespace config store.
func (a *AccessController) watchNamespaceConfigs(ctx context.Context) {

	for {

		// Watch the top 3 most recent changes per namespace.
		//
		// The assumption here is that each peer/node of the cluster should
		// have enough time to have processed one of the last three changes per
		// namespace. At a later date we'll capture metrics around this to quantify
		// a more accurate threshold based on quantitative analysis from running in
		// production.
		iter, err := a.NamespaceManager.TopChanges(context.TODO(), 3)
		if err != nil {
			// todo: handle error
		}

		for iter.Next() {
			change, err := iter.Value()
			if err != nil {
				log.Errorf("Failed to fetch the next value from the ChangelogIterator: %v", err)
			}

			namespace := change.Namespace
			config := change.Config
			timestamp := change.Timestamp

			switch change.Operation {
			case AddNamespace, UpdateNamespace:
				err := peerNamespaceConfigs.SetNamespaceConfigSnapshot(a.ServerID, namespace, config, timestamp)
				if err != nil {
					// todo: handle error
				}
			default:
				panic("An expected namespace operation was encountered")
			}
		}
		if err := iter.Close(ctx); err != nil {
			// todo: handle error
		}

		time.Sleep(2 * time.Second)
	}
}

// chooseNamespaceConfigSnapshot selects the most recent namespace config snapshot that is
// common to all peers/nodes within the cluster that this node is a part of.
func (a *AccessController) chooseNamespaceConfigSnapshot(namespace string) (*NamespaceConfigSnapshot, error) {

	peerSnapshots, err := peerNamespaceConfigs.ListNamespaceConfigSnapshots(namespace)
	if err != nil {
		return nil, err
	}

	min := math.MaxInt32
	var peerWithMin string

	commonTimestamps := map[time.Time]struct{}{}

	if len(peerSnapshots) >= 1 {

		var s map[time.Time]*aclpb.NamespaceConfig

		for peer, snapshots := range peerSnapshots {
			if len(snapshots) < min {
				min = len(snapshots)
				peerWithMin = peer
				s = snapshots
			}
		}

		if len(peerSnapshots) > 1 {
			for timestamp := range s {
				for peer, snapshots := range peerSnapshots {
					if peer != peerWithMin {
						for ts := range snapshots {
							if timestamp.Equal(ts) {
								commonTimestamps[timestamp] = struct{}{}
							}
						}
					}
				}
			}
		} else {
			for ts := range s {
				commonTimestamps[ts] = struct{}{}
			}
		}

		if len(commonTimestamps) < 1 {
			return nil, fmt.Errorf("No common namespace config snapshot timestamp(s) were found for namespace '%s'.", namespace)
		}
	} else {
		return nil, ErrNoLocalNamespacesDefined
	}

	var selectedTS time.Time
	for t := range commonTimestamps {
		if t.After(selectedTS) {
			selectedTS = t
		}
	}

	config := peerSnapshots[peerWithMin][selectedTS]

	snapshot := &NamespaceConfigSnapshot{
		Config:    config,
		Timestamp: selectedTS,
	}

	return snapshot, nil
}

// NewAccessController constructs a new AccessController with the options provided.
func NewAccessController(opts ...AccessControllerOption) (*AccessController, error) {

	ac := AccessController{}

	for _, opt := range opts {
		opt(&ac)
	}

	// Pre-load 3 most recent namespace config changes into the namespace config snapshot store.
	iter, err := ac.NamespaceManager.TopChanges(context.Background(), 3)
	if err != nil {
		return nil, err
	}

	for iter.Next() {
		entry, err := iter.Value()
		if err != nil {
			return nil, err
		}

		switch entry.Operation {
		case AddNamespace, UpdateNamespace:
			err = peerNamespaceConfigs.SetNamespaceConfigSnapshot(ac.ServerID, entry.Namespace, entry.Config, entry.Timestamp)
			if err != nil {
				return nil, err
			}
		default:
			panic("An expected namespace operation was encountered")
		}

	}
	if err := iter.Close(context.Background()); err != nil {
		return nil, err
	}

	// Start watching for namespace configuration changes in the background
	go ac.watchNamespaceConfigs(context.Background())

	ring := consistent.New(nil, consistent.Config{
		Hasher:            &hasher{},
		PartitionCount:    31,
		ReplicationFactor: 3,
		Load:              1.25,
	})

	node := &Node{
		ID:        ac.ServerID,
		RpcRouter: NewMapClientRouter(),
		Hashring: &ConsistentHashring{
			Ring: ring,
		},
	}
	ac.Node = node

	memberlistConfig := memberlist.DefaultLANConfig()
	memberlistConfig.PushPullInterval = 10 * time.Second
	memberlistConfig.Name = node.ID

	if ac.Advertise != "" {
		memberlistConfig.AdvertiseAddr = ac.Advertise
	}

	memberlistConfig.BindPort = ac.NodePort
	memberlistConfig.Events = node
	memberlistConfig.Delegate = &ac

	list, err := memberlist.Create(memberlistConfig)
	if err != nil {
		return nil, err
	}
	node.Memberlist = list

	meta, err := json.Marshal(NodeMetadata{
		ServerPort: ac.ServerPort,
	})
	if err != nil {
		return nil, err
	}

	list.LocalNode().Meta = meta

	if ac.Join != "" {
		joinAddrs := strings.Split(ac.Join, ",")

		if numJoined, err := list.Join(joinAddrs); err != nil {
			if numJoined < 1 {
				// todo: account for this node
				return nil, err
			}
		}
	}

	return &ac, nil
}

func (a *AccessController) checkLeaf(ctx context.Context, op *aclpb.SetOperation_Child, namespace, object, relation, user string) (bool, error) {

	switch rewrite := op.GetChildType().(type) {
	case *aclpb.SetOperation_Child_XThis:
		obj := Object{
			Namespace: namespace,
			ID:        object,
		}

		// do direct check here
		query := RelationTupleQuery{
			Object:    obj,
			Relations: []string{relation},
			Subject:   &SubjectID{ID: user},
		}
		count, _ := a.RelationTupleStore.RowCount(ctx, query)
		// todo: capture error

		if count > 0 {
			return true, nil
		}

		// compute indirect ACLs referenced by subject sets from the tuples
		// SELECT * FROM namespace WHERE relation=<rewrite.relation> AND subject LIKE '_%%:_%%#_%%'
		subjects, _ := a.RelationTupleStore.SubjectSets(ctx, obj, relation)
		// todo: capture error

		for _, subject := range subjects {

			permitted, err := a.check(ctx, subject.Namespace, subject.Object, subject.Relation, user)
			if err != nil {
				return false, err
			}

			if permitted {
				return true, nil
			}
		}

		return false, nil
	case *aclpb.SetOperation_Child_ComputedSubjectset:
		return a.check(ctx, namespace, object, rewrite.ComputedSubjectset.GetRelation(), user)
	case *aclpb.SetOperation_Child_TupleToSubjectset:

		obj := Object{
			Namespace: namespace,
			ID:        object,
		}

		subjects, _ := a.RelationTupleStore.SubjectSets(ctx, obj, rewrite.TupleToSubjectset.GetTupleset().GetRelation())

		for _, subject := range subjects {
			relation := subject.Relation

			if relation == "..." {
				relation = rewrite.TupleToSubjectset.GetComputedSubjectset().GetRelation()
			}

			permitted, err := a.check(ctx, subject.Namespace, subject.Object, relation, user)
			if err != nil {
				return false, err
			}

			if permitted {
				return true, nil
			}
		}

		return false, nil
	}

	return false, nil
}

func (a *AccessController) checkRewrite(ctx context.Context, rule *aclpb.Rewrite, namespace, object, relation, user string) (bool, error) {

	checkOutcomeCh := make(chan bool)
	errCh := make(chan error)

	var wg sync.WaitGroup

	switch o := rule.GetRewriteOperation().(type) {
	case *aclpb.Rewrite_Intersection:

		for _, child := range o.Intersection.GetChildren() {
			wg.Add(1)

			go func(so *aclpb.SetOperation_Child) {
				defer wg.Done()

				var permitted bool
				var err error
				if rewrite := so.GetRewrite(); rewrite != nil {
					permitted, err = a.checkRewrite(ctx, rewrite, namespace, object, relation, user)
				} else {
					permitted, err = a.checkLeaf(ctx, so, namespace, object, relation, user)
				}
				if err != nil {
					errCh <- err
					return
				}

				if !permitted {
					checkOutcomeCh <- false
				}
			}(child)
		}

		go func() {
			wg.Wait()
			checkOutcomeCh <- true
		}()

		select {
		case err := <-errCh:
			return false, err
		case outcome := <-checkOutcomeCh:
			return outcome, nil
		case <-ctx.Done():
			return false, ctx.Err()
		}
	case *aclpb.Rewrite_Union:

		for _, child := range o.Union.GetChildren() {
			wg.Add(1)

			// evaluate each child rule of the expression concurrently
			go func(so *aclpb.SetOperation_Child) {
				defer wg.Done()

				var permitted bool
				var err error
				if rewrite := so.GetRewrite(); rewrite != nil {
					permitted, err = a.checkRewrite(ctx, rewrite, namespace, object, relation, user)

				} else {
					permitted, err = a.checkLeaf(ctx, so, namespace, object, relation, user)
				}
				if err != nil {
					errCh <- err
					return
				}

				if permitted {
					checkOutcomeCh <- true
				}

			}(child)
		}

		go func() {
			wg.Wait()
			checkOutcomeCh <- false
		}()

		select {
		case err := <-errCh:
			return false, err
		case outcome := <-checkOutcomeCh:
			return outcome, nil
		case <-ctx.Done():
			return false, ctx.Err()
		}
	}

	return false, nil
}

func (a *AccessController) check(ctx context.Context, namespace, object, relation, subject string) (bool, error) {

	if peerChecksum, ok := ChecksumFromContext(ctx); ok {
		// The hash ring checksum of the peer should always be present if the
		// request is proxied from another access-controller. If the request
		// is made externally it won't be present.
		if a.Hashring.Checksum() != peerChecksum {
			return false, status.Error(codes.Internal, "Hashring checksums don't match. Retry again soon!")
		}
	}

	var snapshotTimestamp time.Time

	// The namespace config timestamp from the peer should always be present if
	// the request is proxied from another access-controller. If the request is
	// made externally, we select a namespace config timestamp and forward it on.
	peerNamespaceCfgTs, ok := NamespaceConfigTimestampFromContext(ctx)
	if !ok {
		snapshot, err := a.chooseNamespaceConfigSnapshot(namespace)
		if err != nil {
			if err == ErrNoLocalNamespacesDefined {
				return false, fmt.Errorf("No namespace configs have been added yet. Please add a namespace config and then proceed. If you recently added one, it may take a couple minutes to propagate")
			}
			return false, err
		}

		snapshotTimestamp = snapshot.Timestamp
	} else {
		snapshotTimestamp = peerNamespaceCfgTs
	}

	forwardingNodeID := a.Hashring.LocateKey([]byte(object))
	if forwardingNodeID != a.ID {

		log.Tracef("Proxying Check RPC request to node '%v'..", forwardingNodeID)

		c, err := a.RpcRouter.GetClient(forwardingNodeID)
		if err != nil {
			// todo: handle error better
		}

		client, ok := c.(aclpb.CheckServiceClient)
		if !ok {
			// todo: handle error better
		}

		modifiedCtx := context.WithValue(ctx, hashringChecksumKey, a.Hashring.Checksum())
		modifiedCtx = context.WithValue(modifiedCtx, nsConfigSnapshotTimestampKey, snapshotTimestamp)

		subject := SubjectID{ID: subject}

		req := &aclpb.CheckRequest{
			Namespace: namespace,
			Object:    object,
			Relation:  relation,
			Subject:   subject.ToProto(),
		}

		var resp *aclpb.CheckResponse
		resp, err = client.Check(modifiedCtx, req)

		for retries := 0; err != nil && status.Code(err) != codes.Canceled; retries++ {
			log.Tracef("Check proxy RPC failed with error: %v. Retrying..", err)

			if retries > 5 {
				goto EVAL // fallback to evaluating the query locally
			}

			forwardingNodeID := a.Hashring.LocateKey([]byte(object))
			if forwardingNodeID == a.ID {
				goto EVAL
			}

			log.Tracef("Proxying Check RPC request to node '%v'..", forwardingNodeID)

			c, err = a.RpcRouter.GetClient(forwardingNodeID)
			if err != nil {
				continue
			}

			client, ok := c.(aclpb.CheckServiceClient)
			if !ok {
				// todo: handle error better
			}

			modifiedCtx := context.WithValue(ctx, hashringChecksumKey, a.Hashring.Checksum())
			modifiedCtx = context.WithValue(modifiedCtx, nsConfigSnapshotTimestampKey, snapshotTimestamp)

			resp, err = client.Check(modifiedCtx, req)
		}

		if resp != nil {
			return resp.GetAllowed(), nil
		}
	}

EVAL:

	cfg, err := peerNamespaceConfigs.GetNamespaceConfigSnapshot(a.ServerID, namespace, snapshotTimestamp)
	if err != nil {
		return false, err
	}

	if cfg == nil {
		message := fmt.Sprintf("No namespace configuration was found for namespace '%s' at timestamp '%s'", namespace, snapshotTimestamp)
		return false, status.Error(codes.Internal, message) // todo: choose appropriate code here
	}

	rewrite := rewriteFromNamespaceConfig(relation, cfg)
	if rewrite == nil {
		message := fmt.Sprintf("No rewrite snapshot for relation '%s#%s' exists at timestamp '%s'", namespace, relation, snapshotTimestamp)
		return false, status.Error(codes.InvalidArgument, message)
	}

	return a.checkRewrite(ctx, rewrite, namespace, object, relation, subject)
}

func (a *AccessController) expandWithRewrite(ctx context.Context, rewrite *aclpb.Rewrite, tree *Tree, namespace, object, relation string, configSnapshot *NamespaceConfigSnapshot, depth uint) (*Tree, error) {

	op := rewrite.GetRewriteOperation()

	var children []*aclpb.SetOperation_Child
	switch o := op.(type) {
	case *aclpb.Rewrite_Intersection:
		tree.Type = IntersectionNode
		children = o.Intersection.GetChildren()
	case *aclpb.Rewrite_Union:
		tree.Type = UnionNode
		children = o.Union.GetChildren()
	}

	for _, child := range children {

		rewrite := child.GetRewrite()
		if rewrite != nil {
			subTree := &Tree{
				Subject: tree.Subject,
			}

			t, err := a.expandWithRewrite(ctx, rewrite, subTree, namespace, object, relation, configSnapshot, depth)
			if err != nil {
				return nil, err
			}

			tree.Children = append(tree.Children, t)
		} else {

			// otherwise we're dealing with _this, computed_userset, or tuple_to_userset
			switch so := child.GetChildType().(type) {
			case *aclpb.SetOperation_Child_XThis:
				tuples, err := a.RelationTupleStore.ListRelationTuples(ctx, &aclpb.ListRelationTuplesRequest_Query{
					Namespace: namespace,
					Object:    object,
					Relations: []string{relation},
				}, &fieldmaskpb.FieldMask{})
				if err != nil {
					return nil, err
				}

				for _, tuple := range tuples {
					subject := tuple.Subject

					if ss, isSubjectSet := subject.(*SubjectSet); isSubjectSet {

						rr := ss.Relation
						if rr == "..." {
							rr = relation
						}
						t, err := a.expand(ctx, ss.Namespace, ss.Object, rr, configSnapshot, depth-1)
						if err != nil {
							return nil, err
						}

						tree.Children = append(tree.Children, t)
					} else {
						tree.Children = append(tree.Children, &Tree{
							Type:    LeafNode,
							Subject: subject,
						})
					}
				}
			case *aclpb.SetOperation_Child_ComputedSubjectset:
				t, err := a.expand(ctx, namespace, object, so.ComputedSubjectset.GetRelation(), configSnapshot, depth-1)
				if err != nil {
					return nil, err
				}

				tree.Children = append(tree.Children, t)
			case *aclpb.SetOperation_Child_TupleToSubjectset:

				rr := so.TupleToSubjectset.GetTupleset().GetRelation()
				if rr == "..." {
					rr = relation
				}

				tuples, err := a.RelationTupleStore.ListRelationTuples(ctx, &aclpb.ListRelationTuplesRequest_Query{
					Namespace: namespace,
					Object:    object,
					Relations: []string{rr},
				}, &fieldmaskpb.FieldMask{})
				if err != nil {
					return nil, err
				}

				for _, tuple := range tuples {
					subject := tuple.Subject

					if ss, isSubjectSet := subject.(*SubjectSet); isSubjectSet {

						rr := ss.Relation
						if rr == "..." {
							rr = relation
						}
						t, err := a.expand(ctx, ss.Namespace, ss.Object, rr, configSnapshot, depth-1)
						if err != nil {
							return nil, err
						}

						tree.Children = append(tree.Children, t)
					} else {
						tree.Children = append(tree.Children, &Tree{
							Type:    LeafNode,
							Subject: subject,
						})
					}
				}
			}
		}
	}

	return tree, nil
}

func (a *AccessController) expand(ctx context.Context, namespace, object, relation string, configSnapshot *NamespaceConfigSnapshot, depth uint) (*Tree, error) {

	rewrite := rewriteFromNamespaceConfig(relation, configSnapshot.Config)
	if rewrite == nil {
		return nil, fmt.Errorf("Relation '%s' is undefined in namespace '%s' at snapshot config timestamp '%s'", relation, namespace, configSnapshot.Timestamp)
	}

	tree := &Tree{
		Subject: &SubjectSet{
			Namespace: namespace,
			Object:    object,
			Relation:  relation,
		},
	}
	return a.expandWithRewrite(ctx, rewrite, tree, namespace, object, relation, configSnapshot, depth)
}

func (a *AccessController) Check(ctx context.Context, req *aclpb.CheckRequest) (*aclpb.CheckResponse, error) {
	subject := SubjectFromProto(req.GetSubject())

	response := aclpb.CheckResponse{}

	permitted, err := a.check(ctx, req.GetNamespace(), req.GetObject(), req.GetRelation(), subject.String())
	if err != nil {
		return nil, err
	}

	response.Allowed = permitted

	return &response, nil
}

func (a *AccessController) WriteRelationTuplesTxn(ctx context.Context, req *aclpb.WriteRelationTuplesTxnRequest) (*aclpb.WriteRelationTuplesTxnResponse, error) {

	inserts := []*InternalRelationTuple{}
	deletes := []*InternalRelationTuple{}

	for _, delta := range req.GetRelationTupleDeltas() {
		action := delta.GetAction()
		rt := delta.GetRelationTuple()

		namespace := rt.GetNamespace()
		relation := rt.GetRelation()
		subject := SubjectFromProto(rt.GetSubject())

		configSnapshot, err := a.chooseNamespaceConfigSnapshot(namespace)
		if err != nil {
			if err == ErrNoLocalNamespacesDefined {
				return nil, fmt.Errorf("No namespace configs have been added yet. Please add a namespace config and then proceed. If you recently added one, it may take a couple minutes to propagate")
			}

			return nil, fmt.Errorf("'%s' namespace is undefined at this time. If this namespace was recently added, please try again in a couple of minutes.", namespace)
		}

		irt := InternalRelationTuple{
			Namespace: namespace,
			Object:    rt.GetObject(),
			Relation:  relation,
			Subject:   subject,
		}

		switch action {
		case aclpb.RelationTupleDelta_INSERT:

			rewrite := rewriteFromNamespaceConfig(relation, configSnapshot.Config)
			if rewrite == nil {
				return nil, fmt.Errorf("Relation '%s' is undefined in namespace '%s' at snapshot config timestamp '%s'", relation, namespace, configSnapshot.Timestamp)
			}

			switch ref := rt.GetSubject().GetRef().(type) {
			case *aclpb.Subject_Set:
				n := ref.Set.GetNamespace()
				r := ref.Set.GetRelation()

				configSnapshot, err := a.chooseNamespaceConfigSnapshot(n)
				if err != nil {
					return nil, fmt.Errorf("SubjectSet '%s' references the '%s' namespace which is undefined at this time. If this namespace was recently added, please try again in a couple minutes.", subject.String(), n)
				}

				rewrite := rewriteFromNamespaceConfig(r, configSnapshot.Config)
				if rewrite == nil {
					return nil, fmt.Errorf("SubjectSet '%s' references relation '%s' which is undefined in the namespace '%s' at snapshot config timestamp '%s'. If this relation was recently added to the config, please try again in a couple minutes.", subject.String(), r, n, configSnapshot.Timestamp)
				}
			}

			inserts = append(inserts, &irt)
		case aclpb.RelationTupleDelta_DELETE:
			deletes = append(deletes, &irt)
		}
	}

	if err := a.RelationTupleStore.TransactRelationTuples(ctx, inserts, deletes); err != nil {
		return nil, err
	}

	return &aclpb.WriteRelationTuplesTxnResponse{}, nil
}

func (a *AccessController) ListRelationTuples(ctx context.Context, req *aclpb.ListRelationTuplesRequest) (*aclpb.ListRelationTuplesResponse, error) {

	tuples, err := a.RelationTupleStore.ListRelationTuples(ctx, req.GetQuery(), req.GetExpandMask())
	if err != nil {
		return nil, err
	}

	protoTuples := []*aclpb.RelationTuple{}
	for _, tuple := range tuples {
		protoTuples = append(protoTuples, tuple.ToProto())
	}

	response := aclpb.ListRelationTuplesResponse{
		RelationTuples: protoTuples,
	}

	return &response, nil
}

func (a *AccessController) Expand(ctx context.Context, req *aclpb.ExpandRequest) (*aclpb.ExpandResponse, error) {

	subject := req.GetSubjectSet()
	namespace := subject.GetNamespace()
	object := subject.GetObject()
	relation := subject.GetRelation()

	configSnapshot, err := a.chooseNamespaceConfigSnapshot(namespace)
	if err != nil {
		if err == ErrNoLocalNamespacesDefined {
			return nil, fmt.Errorf("No namespace configs have been added yet. Please add a namespace config and then proceed. If you recently added one, it may take a couple minutes to propagate")
		}

		return nil, err
	}

	tree, err := a.expand(ctx, namespace, object, relation, configSnapshot, 12)
	if err != nil {
		return nil, err
	}

	resp := &aclpb.ExpandResponse{
		Tree: tree.ToProto(),
	}

	return resp, nil
}

func (a *AccessController) AddConfig(ctx context.Context, req *aclpb.AddConfigRequest) (*aclpb.AddConfigResponse, error) {

	if req.GetConfig() == nil {
		return nil, status.Error(codes.InvalidArgument, "The 'config' field is required and cannot be nil.")
	}

	if req.GetConfig().GetName() == "" {
		return nil, status.Error(codes.InvalidArgument, "The 'config.name' field is required and cannot be empty.")
	}

	err := a.NamespaceManager.AddConfig(ctx, req.GetConfig())
	if err != nil {
		if err == ErrNamespaceAlreadyExists {
			return nil, status.Error(codes.AlreadyExists, err.Error())
		}

		return nil, err
	}

	return &(aclpb.AddConfigResponse{}), nil
}

func (a *AccessController) ReadConfig(ctx context.Context, req *aclpb.ReadConfigRequest) (*aclpb.ReadConfigResponse, error) {

	if req.GetNamespace() == "" {
		return nil, status.Error(codes.InvalidArgument, "The 'namespace' field is required and cannot be empty.")
	}

	config, err := a.NamespaceManager.GetConfig(ctx, req.GetNamespace())
	if err != nil {
		return nil, err
	}

	resp := &aclpb.ReadConfigResponse{
		Namespace: req.GetNamespace(),
		Config:    config,
	}

	return resp, nil
}

func (a *AccessController) WriteRelation(ctx context.Context, req *aclpb.WriteRelationRequest) (*aclpb.WriteRelationResponse, error) {

	namespace := req.GetNamespace()
	if namespace == "" {
		return nil, status.Error(codes.InvalidArgument, "The 'namespace' field is required and cannot be empty.")
	}

	if req.Relation == nil {
		return nil, status.Error(codes.InvalidArgument, "The 'relation' field must be set. It cannot be nil.")
	}

	if req.GetRelation().GetName() == "" {
		return nil, status.Error(codes.InvalidArgument, "The 'relation.name' field must be set. It cannot be empty.")
	}

	err := a.NamespaceManager.UpsertRelation(ctx, namespace, req.GetRelation())
	if err != nil {
		if err == ErrNamespaceDoesntExist {
			return nil, status.Error(codes.InvalidArgument, fmt.Sprintf("Namespace '%s' doesn't exist. Please add it first.", namespace))
		}
		return nil, err
	}

	return &(aclpb.WriteRelationResponse{}), nil
}

func (a *AccessController) NodeMeta(limit int) []byte {
	var meta []byte
	return meta
}

func (a *AccessController) NotifyMsg(msg []byte) {}

func (a *AccessController) GetBroadcasts(overhead, limit int) [][]byte {
	var buf [][]byte
	return buf
}

// LocalState is used for a TCP Push/Pull between nodes in the cluster. The
// buffer returned here is broadcasted to the other nodes in the cluster
// in addition to the membership information. Any data can be sent here.
//
// For more information see https://pkg.go.dev/github.com/hashicorp/memberlist#Delegate
func (a *AccessController) LocalState(join bool) []byte {

	configs, err := peerNamespaceConfigs.GetNamespaceConfigSnapshots(a.ServerID)
	if err != nil {
		log.Errorf("Failed to fetch namespace config snapshots for this node's LocalState: %v", err)
	}

	if configs == nil {
		panic("This node's local namespace config snapshots do not exist. Something is seriously wrong!")
	}

	meta := NodeMetadata{
		NodeID:                   a.ID,
		ServerPort:               a.ServerPort,
		NamespaceConfigSnapshots: configs,
	}

	data, err := json.Marshal(meta)
	if err != nil {
		log.Errorf("Failed to json.Marshal this node's metadata: %v", err)
	}

	return data
}

// MergeRemoteState is invoked after a TCP Push/Pull between nodes in the cluster.
// This is the state received from the remote node and is the result of the
// remote nodes's LocalState call.
//
// For more information see https://pkg.go.dev/github.com/hashicorp/memberlist#Delegate
func (a *AccessController) MergeRemoteState(buf []byte, join bool) {

	var remoteState NodeMetadata
	if err := json.Unmarshal(buf, &remoteState); err != nil {
		log.Errorf("Failed to json.Unmarshal the remote peer's metadata: %v", err)
	}

	for namespace, configSnapshots := range remoteState.NamespaceConfigSnapshots {

		for ts, config := range configSnapshots {
			err := peerNamespaceConfigs.SetNamespaceConfigSnapshot(remoteState.NodeID, namespace, config, ts)
			if err != nil {
				log.Errorf("Failed to write namespace config snapshot in MergeRemoteState: %v", err)
			}
		}
	}
}

func (a *AccessController) Close() error {
	return a.Node.Memberlist.Leave(5 * time.Second)
}

// rewriteFromNamespaceConfig returns the rewrite rule from the provided namespace config for the
// relation given. If the rewrite rule is 'nil' (e.g. no rewrite rule specified), the _this rule
// is returned, indicating the rewrite is self-referencing by default.
func rewriteFromNamespaceConfig(relation string, config *aclpb.NamespaceConfig) *aclpb.Rewrite {

	for _, r := range config.GetRelations() {

		if r.GetName() == relation {
			rewrite := r.GetRewrite()

			if rewrite == nil {
				rewrite = &aclpb.Rewrite{
					RewriteOperation: &aclpb.Rewrite_Union{
						Union: &aclpb.SetOperation{
							Children: []*aclpb.SetOperation_Child{
								{ChildType: &aclpb.SetOperation_Child_XThis{}},
							},
						},
					},
				}
			}

			return rewrite
		}
	}

	return nil
}
