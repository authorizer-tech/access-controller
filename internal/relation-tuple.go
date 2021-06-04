package accesscontroller

import (
	"encoding/json"
	"fmt"
	"strings"

	pb "github.com/authorizer-tech/access-controller/genprotos/authorizer/accesscontroller/v1alpha1"
)

// ErrInvalidSubjectSetString is an error that is returned if a malformed SubjectSet string
// is encountered.
var ErrInvalidSubjectSetString = fmt.Errorf("the provided SubjectSet string is malformed")

// Object represents a namespace and object id.
type Object struct {
	Namespace string
	ID        string
}

type Subject interface {
	json.Marshaler

	String() string
	FromString(string) (Subject, error)
	Equals(interface{}) bool
	ToProto() *pb.Subject
}

// SubjectID is a unique identifier of some subject.
type SubjectID struct {
	ID string `json:"id"`
}

// MarshalJSON returns the SubjectID as a json byte slice.
func (s SubjectID) MarshalJSON() ([]byte, error) {
	return []byte(`"` + s.String() + `"`), nil
}

// Equals returns a bool indicating if the provided interface and
// the SubjectID are equivalent. Two SubjectIDs are equivalent
// if they have the same ID.
func (s *SubjectID) Equals(v interface{}) bool {
	uv, ok := v.(*SubjectID)
	if !ok {
		return false
	}
	return uv.ID == s.ID
}

// FromString parses str and returns the Subject (SubjectID specifically).
func (s *SubjectID) FromString(str string) (Subject, error) {
	s.ID = str
	return s, nil
}

// String returns the string representation of the SubjectID.
func (s *SubjectID) String() string {
	return s.ID
}

// ToProto returns the protobuf Subject representation of the given SubjectID.
func (s *SubjectID) ToProto() *pb.Subject {

	if s == nil {
		return nil
	}

	return &pb.Subject{
		Ref: &pb.Subject_Id{
			Id: s.ID,
		},
	}
}

// SubjectSet defines the set of all subjects that have a specific relation to an object
// within some namespace.
type SubjectSet struct {
	Namespace string `json:"namespace"`
	Object    string `json:"object"`
	Relation  string `json:"relation"`
}

// Equals returns a bool indicating if the provided interface and
// the SubjectSet are equivalent. Two SubjectSets are equivalent
// if they define the same (namespace, object, relation) tuple.
func (s *SubjectSet) Equals(v interface{}) bool {

	switch ss := v.(type) {
	case *SubjectSet:
		return ss.Relation == s.Relation && ss.Object == s.Object && ss.Namespace == s.Namespace
	case SubjectSet:
		return ss.Relation == s.Relation && ss.Object == s.Object && ss.Namespace == s.Namespace
	}

	return false
}

// String returns the string representation of the SubjectSet.
func (s *SubjectSet) String() string {
	return fmt.Sprintf("%s:%s#%s", s.Namespace, s.Object, s.Relation)
}

// MarshalJSON returns the SubjectSet as a json byte slice.
func (s SubjectSet) MarshalJSON() ([]byte, error) {
	return []byte(`"` + s.String() + `"`), nil
}

// ToProto returns the protobuf Subject representation of the given SubjectSet.
func (s *SubjectSet) ToProto() *pb.Subject {
	if s == nil {
		return nil
	}

	return &pb.Subject{
		Ref: &pb.Subject_Set{
			Set: &pb.SubjectSet{
				Namespace: s.Namespace,
				Object:    s.Object,
				Relation:  s.Relation,
			},
		},
	}
}

// FromString parses str and returns the Subject (SubjectSet specifically)
// or an error if the string was malformed in some way.
func (s *SubjectSet) FromString(str string) (Subject, error) {
	parts := strings.Split(str, "#")
	if len(parts) != 2 {
		return nil, ErrInvalidSubjectSetString
	}

	innerParts := strings.Split(parts[0], ":")
	if len(innerParts) != 2 {
		return nil, ErrInvalidSubjectSetString
	}

	s.Namespace = innerParts[0]
	s.Object = innerParts[1]
	s.Relation = parts[1]

	return s, nil
}

type InternalRelationTuple struct {
	Namespace string  `json:"namespace"`
	Object    string  `json:"object"`
	Relation  string  `json:"relation"`
	Subject   Subject `json:"subject"`
}

// String returns r as a relation tuple in string format.
func (r InternalRelationTuple) String() string {
	return fmt.Sprintf("%s:%s#%s@%s", r.Namespace, r.Object, r.Relation, r.Subject)
}

// ToProto serializes r in it's equivalent protobuf format.
func (r *InternalRelationTuple) ToProto() *pb.RelationTuple {

	if r == nil {
		return nil
	}

	return &pb.RelationTuple{
		Namespace: r.Namespace,
		Object:    r.Object,
		Relation:  r.Relation,
		Subject:   r.Subject.ToProto(),
	}
}

// SubjectSetFromString takes a string `s` and attempts to decode it into
// a SubjectSet (namespace:object#relation). If the string is not formatted
// as a SubjectSet then an error is returned.
func SubjectSetFromString(s string) (SubjectSet, error) {

	subjectSet := SubjectSet{}

	parts := strings.Split(s, "#")
	if len(parts) != 2 {
		return subjectSet, ErrInvalidSubjectSetString
	}

	innerParts := strings.Split(parts[0], ":")
	if len(innerParts) != 2 {
		return subjectSet, ErrInvalidSubjectSetString
	}

	subjectSet.Namespace = innerParts[0]
	subjectSet.Object = innerParts[1]
	subjectSet.Relation = parts[1]

	return subjectSet, nil
}

// SubjectFromString parses the string s and returns a Subject - either
// a SubjectSet or an explicit SubjectID.
func SubjectFromString(s string) (Subject, error) {
	if strings.Contains(s, "#") {
		return (&SubjectSet{}).FromString(s)
	}
	return (&SubjectID{}).FromString(s)
}

// SubjectFromProto deserializes the protobuf subject `sub` into
// it's equivalent Subject structure.
func SubjectFromProto(sub *pb.Subject) Subject {
	switch s := sub.GetRef().(type) {
	case *pb.Subject_Id:
		return &SubjectID{
			ID: s.Id,
		}
	case *pb.Subject_Set:
		return &SubjectSet{
			Namespace: s.Set.Namespace,
			Object:    s.Set.Object,
			Relation:  s.Set.Relation,
		}
	default:
		return nil // impossible to hit this unless we export another unexpected type from the protos
	}
}

type RelationTupleQuery struct {
	Object    Object
	Relations []string
	Subject   Subject
}
