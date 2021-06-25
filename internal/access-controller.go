package accesscontroller

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/hashicorp/memberlist"
	"github.com/pkg/errors"

	aclpb "github.com/authorizer-tech/access-controller/genprotos/authorizer/accesscontroller/v1alpha1"
	log "github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

var (
	internalErrorMessage = "An internal error has occurred. Please try again shortly."
	internalErrorStatus  = status.Error(codes.Internal, internalErrorMessage)
)

type AccessController struct {
	aclpb.UnimplementedCheckServiceServer
	aclpb.UnimplementedWriteServiceServer
	aclpb.UnimplementedReadServiceServer
	aclpb.UnimplementedExpandServiceServer
	aclpb.UnimplementedNamespaceConfigServiceServer

	Memberlist *memberlist.Memberlist
	RPCRouter  ClientRouter
	Hashring   Hashring

	RelationTupleStore
	PeerNamespaceConfigStore
	NamespaceManager
	NodeConfigs

	shutdown chan struct{}
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

	backoffPolicy := backoff.NewExponentialBackOff()
	backoffPolicy.MaxElapsedTime = 90 * time.Second

	go func() {
		for {

			var iter ChangelogIterator
			var err error

			err = backoff.Retry(func() error {

				// Watch the top 3 most recent changes per namespace.
				//
				// The assumption here is that each peer/node of the cluster should
				// have enough time to have processed one of the last three changes per
				// namespace. At a later date we'll capture metrics around this to quantify
				// a more accurate threshold based on quantitative analysis from running in
				// production.
				iter, err = a.NamespaceManager.TopChanges(context.Background(), 3)
				if err != nil {
					log.Error(err)
				}
				return err
			}, backoffPolicy)
			if err != nil {
				log.Errorf("Failed to list top namespace config changes: %v", err)
				continue
			}

			for iter.Next() {
				change, err := iter.Value()
				if err != nil {
					log.Errorf("Failed to fetch the next value from the ChangelogIterator: %v", err)
					break
				}

				namespace := change.Namespace
				config := change.Config
				timestamp := change.Timestamp

				switch change.Operation {
				case AddNamespace, UpdateNamespace:
					err := a.PeerNamespaceConfigStore.SetNamespaceConfigSnapshot(a.ServerID, namespace, config, timestamp)
					if err != nil {
						log.Errorf("Failed to set the namespace config snapshot for this node: %v", err)
					}
				default:
					panic("An expected namespace operation was encountered")
				}
			}
			if err := iter.Close(ctx); err != nil {
				log.Errorf("Failed to close the namespace config iterator: %v", err)
			}

			time.Sleep(2 * time.Second)
		}
	}()

	<-a.shutdown
}

// chooseNamespaceConfigSnapshot selects the most recent namespace config snapshot that is
// common to all peers/nodes within the cluster that this node is a part of.
func (a *AccessController) chooseNamespaceConfigSnapshot(namespace string) (*NamespaceConfigSnapshot, error) {

	peerSnapshots, err := a.PeerNamespaceConfigStore.ListNamespaceConfigSnapshots(namespace)
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
			return nil, ErrNoLocalNamespacesDefined
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

	peerConfigs := &inmemPeerNamespaceConfigStore{
		configs: make(map[string]map[string]map[time.Time]*aclpb.NamespaceConfig),
	}

	ac := AccessController{
		RPCRouter:                NewMapClientRouter(),
		Hashring:                 NewConsistentHashring(nil),
		PeerNamespaceConfigStore: peerConfigs,
		shutdown:                 make(chan struct{}),
	}

	for _, opt := range opts {
		opt(&ac)
	}

	// Pre-load 3 most recent namespace config changes into the namespace config snapshot store.
	iter, err := ac.NamespaceManager.TopChanges(context.Background(), 3)
	if err != nil {
		return nil, errors.WithMessage(err, "Failed to list top namespace changelog changes.")
	}

	for iter.Next() {
		entry, err := iter.Value()
		if err != nil {
			return nil, err
		}

		switch entry.Operation {
		case AddNamespace, UpdateNamespace:
			err = peerConfigs.SetNamespaceConfigSnapshot(ac.ServerID, entry.Namespace, entry.Config, entry.Timestamp)
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

	memberlistConfig := memberlist.DefaultLANConfig()
	memberlistConfig.PushPullInterval = 10 * time.Second
	memberlistConfig.Name = ac.ServerID

	if ac.Advertise != "" {
		memberlistConfig.AdvertiseAddr = ac.Advertise
	}

	memberlistConfig.BindPort = ac.NodePort
	memberlistConfig.Events = &ac
	memberlistConfig.Delegate = &ac

	list, err := memberlist.Create(memberlistConfig)
	if err != nil {
		return nil, err
	}
	ac.Memberlist = list

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
	case *aclpb.SetOperation_Child_This_:
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
		count, err := a.RelationTupleStore.RowCount(ctx, query)
		if err != nil {
			return false, internalErrorStatus
		}

		if count > 0 {
			return true, nil
		}

		// compute indirect ACLs referenced by subject sets from the tuples
		// SELECT * FROM <namespace> WHERE relation=<relation> AND subject LIKE '_%%:_%%#_%%'
		subjects, err := a.RelationTupleStore.SubjectSets(ctx, obj, relation)
		if err != nil {
			return false, internalErrorStatus
		}

		// todo: these checks could be done concurrently to optimize check performance
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

		subjects, err := a.RelationTupleStore.SubjectSets(ctx, obj, rewrite.TupleToSubjectset.GetTupleset().GetRelation())
		if err != nil {
			return false, internalErrorStatus
		}

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
			log.Error("Hashring checksums don't match")
			return false, internalErrorStatus
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
				return false, NamespaceConfigError{
					Message: fmt.Sprintf("'%s' namespace is undefined. If you recently added it, it may take a couple minutes to propagate", namespace),
					Type:    NamespaceDoesntExist,
				}.ToStatus().Err()
			}

			return false, internalErrorStatus
		}

		snapshotTimestamp = snapshot.Timestamp

		ctx = NewContextWithNamespaceConfigTimestamp(ctx, snapshotTimestamp)
	} else {
		snapshotTimestamp = peerNamespaceCfgTs
	}

	cfg, err := a.PeerNamespaceConfigStore.GetNamespaceConfigSnapshot(a.ServerID, namespace, snapshotTimestamp)
	if err != nil {
		return false, internalErrorStatus
	}

	if cfg == nil {
		return false, NamespaceConfigError{
			Message: fmt.Sprintf("'%s' namespace is undefined. If you recently added it, it may take a couple minutes to propagate", namespace),
			Type:    NamespaceDoesntExist,
		}.ToStatus().Err()
	}

	rewrite := rewriteFromNamespaceConfig(relation, cfg)
	if rewrite == nil {
		return false, nil
	}

	forwardingNodeID := a.Hashring.LocateKey([]byte(object)).String()
	if forwardingNodeID != a.ServerID {

		log.Tracef("Proxying Check RPC request to node '%v'..", forwardingNodeID)

		c, err := a.RPCRouter.GetClient(forwardingNodeID)
		if err != nil {
			log.Errorf("Failed to acquire the RPC client for the peer with ID '%s'", forwardingNodeID)
			return false, internalErrorStatus
		}

		client, ok := c.(aclpb.CheckServiceClient)
		if !ok {
			// if this branch is hit there is a serious bug.
			panic("unexpected rpc client type encountered")
		}

		ctx = NewContextWithChecksum(ctx, a.Hashring.Checksum())

		subject := SubjectID{ID: subject}

		req := &aclpb.CheckRequest{
			Namespace: namespace,
			Object:    object,
			Relation:  relation,
			Subject:   subject.ToProto(),
		}

		var resp *aclpb.CheckResponse
		resp, err = client.Check(ctx, req)

		for retries := 0; err != nil && status.Code(err) != codes.Canceled; retries++ {
			log.Tracef("Check proxy RPC failed with error: %v. Retrying..", err)

			if retries > 5 {
				goto EVAL // fallback to evaluating the query locally
			}

			forwardingNodeID := a.Hashring.LocateKey([]byte(object)).String()
			if forwardingNodeID == a.ServerID {
				goto EVAL
			}

			log.Tracef("Proxying Check RPC request to node '%v'..", forwardingNodeID)

			c, err = a.RPCRouter.GetClient(forwardingNodeID)
			if err != nil {
				continue
			}

			client, ok := c.(aclpb.CheckServiceClient)
			if !ok {
				// todo: is panic'ing the right approach here? The value should
				// always be a CheckServiceClient, and if not there's a bug.
				panic("unexpected RPC client type encountered")
			}

			resp, err = client.Check(ctx, req)
		}

		if resp != nil {
			return resp.GetAllowed(), nil
		}
	}

EVAL:
	return a.checkRewrite(ctx, rewrite, namespace, object, relation, subject)
}

func (a *AccessController) expandWithRewrite(ctx context.Context, rewrite *aclpb.Rewrite, tree *SubjectTree, namespace, object, relation string, depth uint) (*SubjectTree, error) {

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
			subTree := &SubjectTree{
				Subject: tree.Subject,
			}

			t, err := a.expandWithRewrite(ctx, rewrite, subTree, namespace, object, relation, depth)
			if err != nil {
				return nil, err
			}

			tree.Children = append(tree.Children, t)
		} else {

			// otherwise we're dealing with _this, computed_userset, or tuple_to_userset
			switch so := child.GetChildType().(type) {
			case *aclpb.SetOperation_Child_This_:
				tuples, err := a.RelationTupleStore.ListRelationTuples(ctx, &aclpb.ListRelationTuplesRequest_Query{
					Namespace: namespace,
					Object:    object,
					Relations: []string{relation},
				}, &fieldmaskpb.FieldMask{})
				if err != nil {
					return nil, internalErrorStatus
				}

				for _, tuple := range tuples {
					subject := tuple.Subject

					if ss, isSubjectSet := subject.(*SubjectSet); isSubjectSet {

						rr := ss.Relation
						if rr == "..." {
							rr = relation
						}
						t, err := a.expand(ctx, ss.Namespace, ss.Object, rr, depth-1)
						if err != nil {
							return nil, err
						}

						if t != nil {
							tree.Children = append(tree.Children, t)
						}
					} else {
						tree.Children = append(tree.Children, &SubjectTree{
							Type:    LeafNode,
							Subject: subject,
						})
					}
				}
			case *aclpb.SetOperation_Child_ComputedSubjectset:
				t, err := a.expand(ctx, namespace, object, so.ComputedSubjectset.GetRelation(), depth-1)
				if err != nil {
					return nil, err
				}

				if t != nil {
					tree.Children = append(tree.Children, t)
				}
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
					return nil, internalErrorStatus
				}

				for _, tuple := range tuples {
					subject := tuple.Subject

					if ss, isSubjectSet := subject.(*SubjectSet); isSubjectSet {

						rr := ss.Relation
						if rr == "..." {
							rr = relation
						}
						t, err := a.expand(ctx, ss.Namespace, ss.Object, rr, depth-1)
						if err != nil {
							return nil, err
						}

						if t != nil {
							tree.Children = append(tree.Children, t)
						}
					} else {
						tree.Children = append(tree.Children, &SubjectTree{
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

func (a *AccessController) expand(ctx context.Context, namespace, object, relation string, depth uint) (*SubjectTree, error) {

	configSnapshot, err := a.chooseNamespaceConfigSnapshot(namespace)
	if err != nil {
		if err == ErrNoLocalNamespacesDefined {
			return nil, NamespaceConfigError{
				Message: fmt.Sprintf("'%s' namespace is undefined. If you recently added it, it may take a couple minutes to propagate", namespace),
				Type:    NamespaceDoesntExist,
			}.ToStatus().Err()
		}

		return nil, internalErrorStatus
	}

	tree := &SubjectTree{
		Subject: &SubjectSet{
			Namespace: namespace,
			Object:    object,
			Relation:  relation,
		},
	}

	rewrite := rewriteFromNamespaceConfig(relation, configSnapshot.Config)
	if rewrite == nil {
		return nil, nil
	}

	ctx = NewContextWithNamespaceConfigTimestamp(ctx, configSnapshot.Timestamp)

	return a.expandWithRewrite(ctx, rewrite, tree, namespace, object, relation, depth)
}

// Check checks if a Subject has a relation to an object within a namespace.
func (a *AccessController) Check(ctx context.Context, req *aclpb.CheckRequest) (*aclpb.CheckResponse, error) {
	subject := SubjectFromProto(req.GetSubject())

	if subject == nil {
		return nil, status.Error(codes.InvalidArgument, "'subject' is a required field")
	}

	response := aclpb.CheckResponse{}

	permitted, err := a.check(ctx, req.GetNamespace(), req.GetObject(), req.GetRelation(), subject.String())
	if err != nil {
		return nil, err
	}

	response.Allowed = permitted

	return &response, nil
}

// WriteRelationTuplesTxn inserts, deletes, or both, within an atomic transaction, one or more relation tuples.
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
			return nil, NamespaceConfigError{
				Message: fmt.Sprintf("'%s' namespace is undefined. If you recently added it, it may take a couple minutes to propagate", namespace),
				Type:    NamespaceDoesntExist,
			}.ToStatus().Err()
		}

		irt := InternalRelationTuple{
			Namespace: namespace,
			Object:    rt.GetObject(),
			Relation:  relation,
			Subject:   subject,
		}

		switch action {
		case aclpb.RelationTupleDelta_ACTION_INSERT:

			rewrite := rewriteFromNamespaceConfig(relation, configSnapshot.Config)
			if rewrite == nil {
				return nil, NamespaceConfigError{
					Message: fmt.Sprintf("'%s' relation is undefined in namespace '%s' at snapshot config timestamp '%s'. If this relation was recently added, please try again in a couple minutes", relation, namespace, configSnapshot.Timestamp),
					Type:    NamespaceRelationUndefined,
				}.ToStatus().Err()
			}

			switch ref := rt.GetSubject().GetRef().(type) {
			case *aclpb.Subject_Set:
				n := ref.Set.GetNamespace()
				r := ref.Set.GetRelation()

				configSnapshot, err := a.chooseNamespaceConfigSnapshot(n)
				if err != nil {
					if err == ErrNoLocalNamespacesDefined {
						return nil, NamespaceConfigError{
							Message: fmt.Sprintf("SubjectSet '%s' references the '%s' namespace which is undefined. If this namespace was recently added, please try again in a couple minutes", subject.String(), n),
							Type:    NamespaceDoesntExist,
						}.ToStatus().Err()
					}

					return nil, internalErrorStatus
				}

				rewrite := rewriteFromNamespaceConfig(r, configSnapshot.Config)
				if rewrite == nil {
					return nil, NamespaceConfigError{
						Message: fmt.Sprintf("SubjectSet '%s' references relation '%s' which is undefined in the namespace '%s' at snapshot config timestamp '%s'. If this relation was recently added to the config, please try again in a couple minutes", subject.String(), r, n, configSnapshot.Timestamp),
						Type:    NamespaceRelationUndefined,
					}.ToStatus().Err()
				}
			}

			inserts = append(inserts, &irt)
		case aclpb.RelationTupleDelta_ACTION_DELETE:
			deletes = append(deletes, &irt)
		}
	}

	if err := a.RelationTupleStore.TransactRelationTuples(ctx, inserts, deletes); err != nil {
		log.Errorf("TransactRelationTuples failed with error: %v", err)

		return nil, internalErrorStatus
	}

	return &aclpb.WriteRelationTuplesTxnResponse{}, nil
}

// ListRelationTuples fetches relation tuples matching the request Query and filters the response fields
// by the provided ExpandMask. No indirect relations are followed, only the explicit relations are
// returned.
func (a *AccessController) ListRelationTuples(ctx context.Context, req *aclpb.ListRelationTuplesRequest) (*aclpb.ListRelationTuplesResponse, error) {

	namespace := req.GetQuery().GetNamespace()
	_, err := a.chooseNamespaceConfigSnapshot(namespace)
	if err != nil {
		return nil, NamespaceConfigError{
			Message: fmt.Sprintf("'%s' namespace is undefined. If you recently added it, it may take a couple minutes to propagate", namespace),
			Type:    NamespaceDoesntExist,
		}.ToStatus().Err()
	}

	tuples, err := a.RelationTupleStore.ListRelationTuples(ctx, req.GetQuery(), req.GetExpandMask())
	if err != nil {
		log.Errorf("ListRelationTuples failed with error: %v", err)

		return nil, internalErrorStatus
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

// Expand expands the provided SubjectSet by traversing all of the subject's (including indirect relations and
// those contributed through rewrites) that have the given relation to the (namespace, object) pair.
func (a *AccessController) Expand(ctx context.Context, req *aclpb.ExpandRequest) (*aclpb.ExpandResponse, error) {

	subject := req.GetSubjectSet()
	namespace := subject.GetNamespace()
	object := subject.GetObject()
	relation := subject.GetRelation()

	tree, err := a.expand(ctx, namespace, object, relation, 12)
	if err != nil {
		return nil, err
	}

	var subjectTree *aclpb.SubjectTree
	if tree != nil {
		subjectTree = tree.ToProto()
	}

	resp := &aclpb.ExpandResponse{
		Tree: subjectTree,
	}

	return resp, nil
}

// WriteConfig upserts the provided namespace configuration. Removing an existing relation that has
// one or more relation tuples referencing it will lead to an error.
func (a *AccessController) WriteConfig(ctx context.Context, req *aclpb.WriteConfigRequest) (*aclpb.WriteConfigResponse, error) {

	config := req.GetConfig()
	if config == nil {
		return nil, status.Error(codes.InvalidArgument, "The 'config' field is required and cannot be nil.")
	}

	namespace := config.GetName()
	if namespace == "" {
		return nil, status.Error(codes.InvalidArgument, "The 'config.name' field is required and cannot be empty.")
	}

	relations := config.GetRelations()

	proposedRelationsMap := map[string]struct{}{}
	for _, relation := range relations {
		proposedRelationsMap[relation.GetName()] = struct{}{}
	}

	for _, relation := range relations {

		rewrite := rewriteFromNamespaceConfig(relation.GetName(), config)
		if err := validateRewriteRelations(proposedRelationsMap, rewrite); err != nil {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
	}

	err := a.NamespaceManager.WrapTransaction(ctx, func(txnCtx context.Context) error {
		currentConfig, err := a.NamespaceManager.GetConfig(txnCtx, namespace)
		if err != nil {
			if err == ErrNamespaceDoesntExist {
				return a.NamespaceManager.UpsertConfig(txnCtx, config)
			}

			return err
		}

		currentRelationsMap := map[string]struct{}{}
		for _, relation := range currentConfig.GetRelations() {
			currentRelationsMap[relation.GetName()] = struct{}{}
		}

		removedRelations := []string{}
		for relationName := range currentRelationsMap {
			if _, ok := proposedRelationsMap[relationName]; !ok {
				removedRelations = append(removedRelations, relationName)
			}
		}

		if len(removedRelations) > 0 {

			references, err := a.NamespaceManager.LookupRelationReferencesByCount(txnCtx, namespace, removedRelations...)
			if err != nil {
				return err
			}

			if len(references) > 0 {
				relations := []string{}
				for relation := range references {
					relations = append(relations, relation)
				}

				return NamespaceConfigError{
					Message: fmt.Sprintf("Relation(s) [%v] cannot be removed while one or more relation tuples reference them. Please migrate all relation tuples before removing a relation.", strings.Join(relations, ",")),
					Type:    NamespaceUpdateFailedPrecondition,
				}
			}
		}

		return a.NamespaceManager.UpsertConfig(txnCtx, config)
	})
	if err != nil {
		err, ok := err.(NamespaceConfigError)
		if ok {
			return nil, err.ToStatus().Err()
		}

		return nil, internalErrorStatus
	}

	return &(aclpb.WriteConfigResponse{}), nil
}

// ReadConfig fetches the namespace configuration for the provided namespace. If the namespace
// does not exist, an error is returned.
func (a *AccessController) ReadConfig(ctx context.Context, req *aclpb.ReadConfigRequest) (*aclpb.ReadConfigResponse, error) {

	namespace := req.GetNamespace()
	if namespace == "" {
		return nil, status.Error(codes.InvalidArgument, "The 'namespace' field is required and cannot be empty.")
	}

	config, err := a.NamespaceManager.GetConfig(ctx, namespace)
	if err != nil {
		if errors.Is(err, ErrNamespaceDoesntExist) {
			return nil, status.Errorf(codes.NotFound, "The namespace '%s' does not exist. If it was recently added, please try again in a couple of minutes", namespace)
		}
		return nil, internalErrorStatus
	}

	resp := &aclpb.ReadConfigResponse{
		Namespace: namespace,
		Config:    config,
	}

	return resp, nil
}

// NodeMeta is used to retrieve meta-data about the current node
// when broadcasting an alive message to other peers. It's length
// is limited to the given byte size.
//
// For more information see https://pkg.go.dev/github.com/hashicorp/memberlist#Delegate
func (a *AccessController) NodeMeta(limit int) []byte {

	meta, err := json.Marshal(NodeMetadata{
		ServerPort: a.ServerPort,
	})
	if err != nil {
		log.Errorf("Failed to json.Marshal this node's metadata: %v", err)
		return nil
	}

	return meta
}

// NotifyMsg is called when a user-data message is received from a peer. Care
// should be taken that this method does not block, since doing so would block
// the entire UDP packet receive loop in the gossip. Additionally, the byte slice
// may be modified after the call returns, so it should be copied if needed.
//
// For more information see https://pkg.go.dev/github.com/hashicorp/memberlist#Delegate
func (a *AccessController) NotifyMsg(msg []byte) {}

// GetBroadcasts is called when user-data messages can be broadcast to peers.
// It should return a list of buffers to send. Each buffer should assume an
// overhead as provided with a limit on the total byte size allowed. The total
// byte size of the resulting data to send must not exceed the limit. Care
// should be taken that this method does not block, since doing so would block
// the entire UDP packet receive loop.
//
// For more information see https://pkg.go.dev/github.com/hashicorp/memberlist#Delegate
func (a *AccessController) GetBroadcasts(overhead, limit int) [][]byte {
	var buf [][]byte
	return buf
}

// LocalState is used for a TCP Push/Pull between nodes in the cluster. The
// buffer returned here is broadcasted to the other peers in the cluster in
// addition to the membership information. Any data can be sent here.
//
// For more information see https://pkg.go.dev/github.com/hashicorp/memberlist#Delegate
func (a *AccessController) LocalState(join bool) []byte {

	configs, err := a.PeerNamespaceConfigStore.GetNamespaceConfigSnapshots(a.ServerID)
	if err != nil {
		log.Errorf("Failed to fetch namespace config snapshots for this node's LocalState: %v", err)
	}

	if configs == nil {
		panic("This node's local namespace config snapshots do not exist. Something is seriously wrong!")
	}

	serializedConfigs := map[string]map[time.Time][]byte{}
	for namespace, snapshots := range configs {
		for timestamp, config := range snapshots {
			bytes, err := protojson.Marshal(config)
			if err != nil {
				log.Errorf("Failed to protojson.Marshal the namespace configuration: %v", err)
			}

			if _, ok := serializedConfigs[namespace]; !ok {
				serializedConfigs[namespace] = map[time.Time][]byte{
					timestamp: bytes,
				}
			} else {
				serializedConfigs[namespace][timestamp] = bytes
			}
		}
	}

	meta := NodeMetadata{
		NodeID:                   a.ServerID,
		ServerPort:               a.ServerPort,
		NamespaceConfigSnapshots: serializedConfigs,
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
		return
	}

	for namespace, configSnapshots := range remoteState.NamespaceConfigSnapshots {

		for ts, config := range configSnapshots {

			var deserializedConfig aclpb.NamespaceConfig
			if err := protojson.Unmarshal(config, &deserializedConfig); err != nil {
				log.Errorf("Failed to protojson.Unmarshal a remote peer's namespace config snapshot: %v", err)
			} else {
				err := a.PeerNamespaceConfigStore.SetNamespaceConfigSnapshot(remoteState.NodeID, namespace, &deserializedConfig, ts)
				if err != nil {
					log.Errorf("Failed to write namespace config snapshot in MergeRemoteState: %v", err)
				}
			}
		}
	}
}

// NotifyJoin is invoked when a new node has joined the cluster.
// The `member` argument must not be modified.
func (a *AccessController) NotifyJoin(member *memberlist.Node) {

	log.Infof("Cluster member with id '%s' joined the cluster at address '%s'", member.String(), member.FullAddress().Addr)

	nodeID := member.String()
	if nodeID != a.ServerID {
		var meta NodeMetadata
		if err := json.Unmarshal(member.Meta, &meta); err != nil {
			log.Errorf("Failed to json.Unmarshal the Node metadata: %v", err)
			return
		}

		remoteAddr := fmt.Sprintf("%s:%d", member.Addr, meta.ServerPort)

		opts := []grpc.DialOption{
			grpc.WithInsecure(),
		}
		conn, err := grpc.Dial(remoteAddr, opts...)
		if err != nil {
			log.Errorf("Failed to establish a grpc connection to cluster member '%s' at address '%s'", nodeID, remoteAddr)
			return
		}

		client := aclpb.NewCheckServiceClient(conn)

		a.RPCRouter.AddClient(nodeID, client)
	}

	a.Hashring.Add(member)
	log.Tracef("hashring checksum: %d", a.Hashring.Checksum())
}

// NotifyLeave is invoked when a node leaves the cluster. The
// `member` argument must not be modified.
func (a *AccessController) NotifyLeave(member *memberlist.Node) {

	log.Infof("Cluster member with id '%v' at address '%v' left the cluster", member.String(), member.FullAddress().Addr)

	nodeID := member.String()
	if nodeID != a.ServerID {
		a.RPCRouter.RemoveClient(nodeID)
	}

	a.Hashring.Remove(member)
	log.Tracef("hashring checksum: %d", a.Hashring.Checksum())

	err := a.PeerNamespaceConfigStore.DeleteNamespaceConfigSnapshots(nodeID)
	if err != nil {
		log.Errorf("Failed to delete namespace config snapshots for peer with id '%s'", nodeID)
	}
}

// NotifyUpdate is invoked when a node in the cluster is updated,
// usually involving the meta-data of the node. The `member` argument
// must not be modified.
func (a *AccessController) NotifyUpdate(member *memberlist.Node) {}

// Close terminates this access-controller's cluster membership and gracefully closes
// any remaining resources.
func (a *AccessController) Close() error {
	a.shutdown <- struct{}{}

	return a.Memberlist.Leave(5 * time.Second)
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
								{ChildType: &aclpb.SetOperation_Child_This_{}},
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

// validateRewriteRelations validates the relations in the provided rewrite rule reference those
// specified in the definedRelations. If a relation in the rewrite rule references a undefined
// relation, an error is returned.
func validateRewriteRelations(definedRelations map[string]struct{}, rewrite *aclpb.Rewrite) error {

	op := rewrite.GetRewriteOperation()

	var children []*aclpb.SetOperation_Child
	switch o := op.(type) {
	case *aclpb.Rewrite_Intersection:
		children = o.Intersection.GetChildren()
	case *aclpb.Rewrite_Union:
		children = o.Union.GetChildren()
	default:
		panic("Unexpected rewrite operation encountered")
	}

	for _, child := range children {
		rewr := child.GetRewrite()
		if rewr != nil {
			return validateRewriteRelations(definedRelations, rewr)
		} else {
			setOperation := child.GetChildType()
			switch so := setOperation.(type) {
			case *aclpb.SetOperation_Child_This_:
				continue
			case *aclpb.SetOperation_Child_ComputedSubjectset:
				relation := so.ComputedSubjectset.GetRelation()

				if _, ok := definedRelations[relation]; !ok {
					return fmt.Errorf("'%s' relation is referenced but undefined in the provided namespace config", relation)
				}
			case *aclpb.SetOperation_Child_TupleToSubjectset:
				relation := so.TupleToSubjectset.Tupleset.Relation

				if _, ok := definedRelations[relation]; !ok {
					return fmt.Errorf("'%s' relation is referenced but undefined in the provided namespace config", relation)
				}
			default:
				panic("Unexpected rewrite set operation type")
			}
		}
	}

	return nil
}
