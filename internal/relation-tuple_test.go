package accesscontroller

import (
	"errors"
	"testing"

	aclpb "github.com/authorizer-tech/access-controller/genprotos/authorizer/accesscontroller/v1alpha1"
	"google.golang.org/protobuf/proto"
)

func TestSubjectID_ToProto(t *testing.T) {

	tests := []struct {
		input  *SubjectID
		output *aclpb.Subject
	}{
		{
			output: nil,
		},
		{
			input: &SubjectID{"user1"},
			output: &aclpb.Subject{
				Ref: &aclpb.Subject_Id{Id: "user1"},
			},
		},
	}

	for _, test := range tests {
		actual := test.input.ToProto()

		if !proto.Equal(actual, test.output) {
			t.Errorf("Expected '%v', but got '%v'", test.output, actual)
		}
	}
}

func TestSubjectID_Equals(t *testing.T) {

	tests := []struct {
		input  interface{}
		output bool
	}{
		{
			input:  "some-string",
			output: false,
		},
		{
			input:  &SubjectID{"user2"},
			output: false,
		},
		{
			input:  &SubjectID{"user1"},
			output: true,
		},
	}

	for _, test := range tests {
		actual := (&SubjectID{"user1"}).Equals(test.input)

		if actual != test.output {
			t.Errorf("Expected '%v', but got '%v'", test.output, actual)
		}
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
		input  *SubjectSet
		output *aclpb.Subject
	}{
		{
			output: nil,
		},
		{
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
		actual := test.input.ToProto()

		if !proto.Equal(actual, test.output) {
			t.Errorf("Expected '%v', but got '%v'", test.output, actual)
		}
	}
}

func TestInternalRelationTuple_ToProto(t *testing.T) {

	tests := []struct {
		input  *InternalRelationTuple
		output *aclpb.RelationTuple
	}{
		{
			output: nil,
		},
		{
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
		actual := test.input.ToProto()

		if !proto.Equal(actual, test.output) {
			t.Errorf("Expected '%v', but got '%v'", test.output, actual)
		}
	}
}

func TestSubjectFromString(t *testing.T) {

	type output struct {
		subject Subject
		err     error
	}

	tests := []struct {
		input  string
		output output
	}{
		{
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
			input: "user1",
			output: output{
				subject: &SubjectID{"user1"},
			},
		},
		{
			input: "group1#",
			output: output{
				err: ErrInvalidSubjectSetString,
			},
		},
	}

	for _, test := range tests {

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
	}
}
