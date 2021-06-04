package accesscontroller

import (
	"errors"
	"reflect"
	"testing"

	aclpb "github.com/authorizer-tech/access-controller/genprotos/authorizer/accesscontroller/v1alpha1"
	"google.golang.org/protobuf/proto"
)

func TestSubjectID_ToProto(t *testing.T) {

	tests := []struct {
		name   string
		input  *SubjectID
		output *aclpb.Subject
	}{
		{
			name:   "Test-1",
			output: nil,
		},
		{
			name:  "Test-2",
			input: &SubjectID{"user1"},
			output: &aclpb.Subject{
				Ref: &aclpb.Subject_Id{Id: "user1"},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			actual := test.input.ToProto()

			if !proto.Equal(actual, test.output) {
				t.Errorf("Expected '%v', but got '%v'", test.output, actual)
			}
		})
	}
}

func TestSubjectID_MarshalJSON(t *testing.T) {

	type output struct {
		bytes []byte
		err   error
	}

	tests := []struct {
		name   string
		input  SubjectID
		output output
	}{
		{
			name:  "Test-1",
			input: SubjectID{"user1"},
			output: output{
				bytes: []byte(`"user1"`),
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			actual, err := test.input.MarshalJSON()

			if !errors.Is(err, test.output.err) {
				t.Errorf("Errors were not equal. Expected '%v', but got '%v'", test.output.err, err)
			} else {
				if err == nil {
					if !reflect.DeepEqual(actual, test.output.bytes) {
						t.Errorf("Expected '%s', but got '%s'", test.output.bytes, actual)
					}
				}
			}
		})
	}
}

func TestSubjectID_Equals(t *testing.T) {

	tests := []struct {
		name   string
		input  interface{}
		output bool
	}{
		{
			name:   "Test-1",
			input:  "some-string",
			output: false,
		},
		{
			name:   "Test-2",
			input:  &SubjectID{"user2"},
			output: false,
		},
		{
			name:   "Test-3",
			input:  &SubjectID{"user1"},
			output: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			actual := (&SubjectID{"user1"}).Equals(test.input)

			if actual != test.output {
				t.Errorf("Expected '%v', but got '%v'", test.output, actual)
			}
		})
	}
}

func TestSubjectID_String(t *testing.T) {

	s := &SubjectID{"user1"}
	if s.String() != "user1" {
		t.Errorf("Expected 'user1', but got '%s'", s)
	}

	s = &SubjectID{}
	if s.String() != "" {
		t.Errorf("Expected empty string, but got '%s'", s)
	}
}

func TestSubjectSet_ToProto(t *testing.T) {

	tests := []struct {
		name   string
		input  *SubjectSet
		output *aclpb.Subject
	}{
		{
			name:   "Test-1",
			output: nil,
		},
		{
			name: "Test-2",
			input: &SubjectSet{
				Namespace: "namespace1",
				Object:    "object1",
				Relation:  "relation1",
			},
			output: &aclpb.Subject{
				Ref: &aclpb.Subject_Set{
					Set: &aclpb.SubjectSet{
						Namespace: "namespace1",
						Object:    "object1",
						Relation:  "relation1",
					},
				},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			actual := test.input.ToProto()

			if !proto.Equal(actual, test.output) {
				t.Errorf("Expected '%v', but got '%v'", test.output, actual)
			}
		})
	}
}

func TestSubjectSet_String(t *testing.T) {

	s := &SubjectSet{
		Namespace: "namespace1",
		Object:    "object1",
		Relation:  "relation1",
	}
	if s.String() != "namespace1:object1#relation1" {
		t.Errorf("Expected 'namespace1:object1#relation1', but got '%s'", s)
	}

	s = &SubjectSet{}
	if s.String() != ":#" {
		t.Errorf("Expected ':#' string, but got '%s'", s)
	}
}

func TestSubjectSet_MarshalJSON(t *testing.T) {

	type output struct {
		bytes []byte
		err   error
	}

	tests := []struct {
		name   string
		input  SubjectSet
		output output
	}{
		{
			name: "Test-1",
			input: SubjectSet{
				Namespace: "namespace1",
				Object:    "object1",
				Relation:  "relation1",
			},
			output: output{
				bytes: []byte(`"namespace1:object1#relation1"`),
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			actual, err := test.input.MarshalJSON()

			if !errors.Is(err, test.output.err) {
				t.Errorf("Errors were not equal. Expected '%v', but got '%v'", test.output.err, err)
			} else {
				if err == nil {
					if !reflect.DeepEqual(actual, test.output.bytes) {
						t.Errorf("Expected '%s', but got '%s'", test.output.bytes, actual)
					}
				}
			}
		})
	}
}

func TestSubjectSet_Equals(t *testing.T) {

	ss := &SubjectSet{
		Namespace: "namespace1",
		Object:    "object1",
		Relation:  "relation1",
	}

	tests := []struct {
		name   string
		input  interface{}
		output bool
	}{
		{
			name:   "Test-1",
			input:  "some-string",
			output: false,
		},
		{
			name:   "Test-2",
			input:  &SubjectSet{},
			output: false,
		},
		{
			name: "Test-3",
			input: &SubjectSet{
				Namespace: "namespace1",
				Object:    "object1",
				Relation:  "relation1",
			},
			output: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			actual := ss.Equals(test.input)

			if actual != test.output {
				t.Errorf("Expected '%v', but got '%v'", test.output, actual)
			}
		})
	}
}

func TestSubjectSet_FromString(t *testing.T) {

	type output struct {
		subject Subject
		err     error
	}

	tests := []struct {
		name   string
		s      string
		output output
	}{
		{
			name: "Test-1",
			s:    "bad",
			output: output{
				err: ErrInvalidSubjectSetString,
			},
		},
		{
			name: "Test-2",
			s:    "namespace1:object1#relation1",
			output: output{
				subject: &SubjectSet{
					Namespace: "namespace1",
					Object:    "object1",
					Relation:  "relation1",
				},
			},
		},
		{
			name: "Test-3",
			s:    "part1#relation1",
			output: output{
				err: ErrInvalidSubjectSetString,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ss := SubjectSet{}
			subject, err := ss.FromString(test.s)

			if err != test.output.err {
				t.Errorf("Expected error '%v', but got '%v'", test.output.err, err)
			} else {
				if err == nil {
					if !test.output.subject.Equals(subject) {
						t.Errorf("Expected subject '%v', but got '%v'", test.output.subject, subject)
					}
				}
			}
		})
	}
}

func TestInternalRelationTuple_ToProto(t *testing.T) {

	tests := []struct {
		name   string
		input  *InternalRelationTuple
		output *aclpb.RelationTuple
	}{
		{
			name:   "Test-1",
			output: nil,
		},
		{
			name: "Test-2",
			input: &InternalRelationTuple{
				Namespace: "namespace1",
				Object:    "object1",
				Relation:  "relation1",
				Subject:   &SubjectID{"user1"},
			},
			output: &aclpb.RelationTuple{
				Namespace: "namespace1",
				Object:    "object1",
				Relation:  "relation1",
				Subject: &aclpb.Subject{
					Ref: &aclpb.Subject_Id{Id: "user1"},
				},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			actual := test.input.ToProto()

			if !proto.Equal(actual, test.output) {
				t.Errorf("Expected '%v', but got '%v'", test.output, actual)
			}
		})
	}
}

func TestInternalRelationTuple_String(t *testing.T) {

	s := InternalRelationTuple{
		Namespace: "namespace1",
		Object:    "object1",
		Relation:  "relation1",
		Subject:   &SubjectID{"user1"},
	}
	if s.String() != "namespace1:object1#relation1@user1" {
		t.Errorf("Expected 'namespace1:object1#relation1@user1', but got '%s'", s)
	}

	s = InternalRelationTuple{
		Namespace: "namespace1",
		Object:    "object1",
		Relation:  "relation1",
		Subject: &SubjectSet{
			Namespace: "namespace2",
			Object:    "object2",
			Relation:  "relation2",
		},
	}
	if s.String() != "namespace1:object1#relation1@namespace2:object2#relation2" {
		t.Errorf("Expected 'namespace1:object1#relation1@namespace2:object2#relation2' string, but got '%s'", s)
	}
}

func TestSubjectFromString(t *testing.T) {

	type output struct {
		subject Subject
		err     error
	}

	tests := []struct {
		name   string
		input  string
		output output
	}{
		{
			name:  "Test-1",
			input: "groups:group1#member",
			output: output{
				subject: &SubjectSet{
					Namespace: "groups",
					Object:    "group1",
					Relation:  "member",
				},
			},
		},
		{
			name:  "Test-2",
			input: "user1",
			output: output{
				subject: &SubjectID{"user1"},
			},
		},
		{
			name:  "Test-3",
			input: "group1#",
			output: output{
				err: ErrInvalidSubjectSetString,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			subject, err := SubjectFromString(test.input)

			if !errors.Is(err, test.output.err) {
				t.Errorf("Errors were not equal. Expected '%v', but got '%v'", test.output.err, err)
			} else {
				if err == nil {
					if !subject.Equals(test.output.subject) {
						t.Errorf("Subjects were not equal. Expected '%v', but got '%v'", test.output.subject, subject)
					}
				}
			}
		})
	}
}

func TestSubjectSetFromString(t *testing.T) {

	type output struct {
		subject SubjectSet
		err     error
	}

	tests := []struct {
		name  string
		input string
		output
	}{
		{
			name:  "Test-1",
			input: "bad",
			output: output{
				err: ErrInvalidSubjectSetString,
			},
		},
		{
			name:  "Test-2",
			input: "invalidpart1#part2",
			output: output{
				err: ErrInvalidSubjectSetString,
			},
		},
		{
			name:  "Test-3",
			input: "namespace1:object1#relation1",
			output: output{
				subject: SubjectSet{
					Namespace: "namespace1",
					Object:    "object1",
					Relation:  "relation1",
				},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ss, err := SubjectSetFromString(test.input)

			if !errors.Is(err, test.output.err) {
				t.Errorf("Expected error '%v', but got '%v'", test.output.err, err)
			} else {
				if err == nil {
					if !ss.Equals(test.output.subject) {
						t.Errorf("Expected '%s', but got '%s'", test.output.subject, ss)
					}
				}
			}
		})
	}
}

func TestSubjectFromProto(t *testing.T) {

	tests := []struct {
		name   string
		input  *aclpb.Subject
		output Subject
	}{
		{
			name: "Test-1",
			input: &aclpb.Subject{
				Ref: &aclpb.Subject_Id{Id: "user1"},
			},
			output: &SubjectID{"user1"},
		},
		{
			name: "Test-2",
			input: &aclpb.Subject{
				Ref: &aclpb.Subject_Set{
					Set: &aclpb.SubjectSet{
						Namespace: "namespace1",
						Object:    "object1",
						Relation:  "relation1",
					},
				},
			},
			output: &SubjectSet{
				Namespace: "namespace1",
				Object:    "object1",
				Relation:  "relation1",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			subject := SubjectFromProto(test.input)

			if !subject.Equals(test.output) {
				t.Errorf("Expected subject '%v', but got '%v'", test.output, subject)
			}
		})
	}
}
