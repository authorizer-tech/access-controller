package datastores

import (
	"context"
	"database/sql"

	"github.com/doug-martin/goqu/v9"
	_ "github.com/doug-martin/goqu/v9/dialect/postgres" // use postgres dialect driver
	"github.com/doug-martin/goqu/v9/exp"
	_ "github.com/lib/pq"

	aclpb "github.com/authorizer-tech/access-controller/genprotos/authorizer/accesscontroller/v1alpha1"
	ac "github.com/authorizer-tech/access-controller/internal"
)

// SQLStore implements the RelationTupleStore interface for a sql storage adapter.
type SQLStore struct {
	DB *sql.DB
}

// SubjectSets fetches the subject sets for all of the (object, relation) pairs provided.
func (s *SQLStore) SubjectSets(ctx context.Context, object ac.Object, relations ...string) ([]ac.SubjectSet, error) {

	sqlbuilder := goqu.Dialect("postgres").From(object.Namespace).Select("subject").Where(
		goqu.Ex{
			"object":   object.ID,
			"relation": relations,
			"subject":  goqu.Op{"like": "_%%:_%%#_%%"},
		},
	)

	sql, args, err := sqlbuilder.ToSQL()
	if err != nil {
		return nil, err
	}

	rows, err := s.DB.Query(sql, args...)
	if err != nil {
		return nil, err
	}

	subjects := []ac.SubjectSet{}
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}

		subjectSet, err := ac.SubjectSetFromString(s)
		if err != nil {
			return nil, err
		}

		subjects = append(subjects, subjectSet)
	}
	rows.Close()

	return subjects, nil
}

// RowCount returns the number of rows matching the relation tuple query provided.
func (s *SQLStore) RowCount(ctx context.Context, query ac.RelationTupleQuery) (int64, error) {

	sqlbuilder := goqu.Dialect("postgres").From(query.Object.Namespace).Select(
		goqu.COUNT("*"),
	).Where(goqu.Ex{
		"object":   query.Object.ID,
		"relation": query.Relations,
		"subject":  query.Subject.String(),
	})

	sql, args, err := sqlbuilder.ToSQL()
	if err != nil {
		return -1, err
	}

	row := s.DB.QueryRow(sql, args...)

	var count int64
	if err := row.Scan(&count); err != nil {
		return -1, err
	}

	return count, nil
}

// ListRelationTuples lists the relation tuples matching the request query and filters the response fields
// by the provided field mask.
func (s *SQLStore) ListRelationTuples(ctx context.Context, query *aclpb.ListRelationTuplesRequest_Query) ([]ac.InternalRelationTuple, error) {

	sqlbuilder := goqu.Dialect("postgres").From(query.GetNamespace()).Prepared(true)

	if query.GetObject() != "" {
		sqlbuilder = sqlbuilder.Where(goqu.Ex{
			"object": query.GetObject(),
		})
	}

	if len(query.GetRelations()) > 0 {
		sqlbuilder = sqlbuilder.Where(goqu.Ex{
			"relation": query.GetRelations(),
		})
	}

	if query.GetSubject() != nil {
		sqlbuilder = sqlbuilder.Where(goqu.Ex{
			"subject": query.GetSubject().String(),
		})
	}

	sql, args, err := sqlbuilder.ToSQL()
	if err != nil {
		return nil, err
	}

	rows, err := s.DB.Query(sql, args...)
	if err != nil {
		return nil, err
	}

	tuples := []ac.InternalRelationTuple{}
	for rows.Next() {
		var object, relation, s string
		if err := rows.Scan(&object, &relation, &s); err != nil {
			return nil, err
		}

		subject, err := ac.SubjectFromString(s)
		if err != nil {
			return nil, err
		}

		tuples = append(tuples, ac.InternalRelationTuple{
			Namespace: query.GetNamespace(),
			Object:    object,
			Relation:  relation,
			Subject:   subject,
		})
	}
	rows.Close()

	return tuples, nil
}

// TransactRelationTuples applies, with the same txn, the relation tuple inserts and deletions provided.
// Each insertion/deletion includes a corresponding changelog entry with an operation indicating what was
// applied.
func (s *SQLStore) TransactRelationTuples(ctx context.Context, tupleInserts []*ac.InternalRelationTuple, tupleDeletes []*ac.InternalRelationTuple) error {

	txn, err := s.DB.Begin()
	if err != nil {
		return err
	}

	for _, tuple := range tupleInserts {
		sqlbuilder := goqu.Dialect("postgres").Insert(tuple.Namespace).Cols("object", "relation", "subject").Vals(
			goqu.Vals{tuple.Object, tuple.Relation, tuple.Subject.String()},
		).OnConflict(goqu.DoNothing())

		sql, args, err := sqlbuilder.ToSQL()
		if err != nil {
			return err
		}

		_, err = txn.Exec(sql, args...)
		if err != nil {
			return err
		}

		sql, args, err = goqu.Dialect("postgres").Insert("changelog").Cols(
			"namespace", "operation", "relationtuple", "timestamp",
		).Vals(
			goqu.Vals{tuple.Namespace, "INSERT", tuple.String(), goqu.L("NOW()")},
		).OnConflict(
			goqu.DoNothing(),
		).ToSQL()
		if err != nil {
			return err
		}

		_, err = txn.Exec(sql, args...)
		if err != nil {
			return err
		}

		sqlbuilder = goqu.Dialect("postgres").Insert("namespace-relation-lookup").Cols(
			"namespace", "relation", "relationtuple",
		).OnConflict(goqu.DoNothing())

		values := [][]interface{}{
			goqu.Vals{tuple.Namespace, tuple.Relation, tuple.String()},
		}

		if subjectSet, ok := tuple.Subject.(*ac.SubjectSet); ok {
			values = append(values,
				goqu.Vals{subjectSet.Namespace, subjectSet.Relation, tuple.String()},
			)
		}

		sqlbuilder = sqlbuilder.Vals(values...)

		sql, args, err = sqlbuilder.ToSQL()
		if err != nil {
			return err
		}

		_, err = txn.Exec(sql, args...)
		if err != nil {
			return err
		}
	}

	for _, tuple := range tupleDeletes {
		sqlbuilder := goqu.Dialect("postgres").Delete(tuple.Namespace).Where(goqu.Ex{
			"object":   tuple.Object,
			"relation": tuple.Relation,
			"subject":  tuple.Subject.String(),
		})

		sql, args, err := sqlbuilder.ToSQL()
		if err != nil {
			return err
		}

		_, err = txn.Exec(sql, args...)
		if err != nil {
			return err
		}

		expressions := []exp.Expression{
			goqu.Ex{
				"namespace":     tuple.Namespace,
				"relation":      tuple.Relation,
				"relationtuple": tuple.String(),
			},
		}

		if subjectSet, ok := tuple.Subject.(*ac.SubjectSet); ok {
			expressions = append(expressions,
				goqu.Ex{
					"namespace":     subjectSet.Namespace,
					"relation":      subjectSet.Relation,
					"relationtuple": tuple.String(),
				},
			)
		}

		sqlbuilder = goqu.Dialect("postgres").Delete("namespace-relation-lookup").Where(expressions...)

		sql, args, err = sqlbuilder.ToSQL()
		if err != nil {
			return err
		}

		_, err = txn.Exec(sql, args...)
		if err != nil {
			return err
		}

		sql, args, err = goqu.Dialect("postgres").Insert("changelog").Cols(
			"namespace", "operation", "relationtuple", "timestamp",
		).Vals(
			goqu.Vals{tuple.Namespace, "DELETE", tuple.String(), goqu.L("NOW()")},
		).OnConflict(
			goqu.DoNothing(),
		).ToSQL()
		if err != nil {
			return err
		}

		_, err = txn.Exec(sql, args...)
		if err != nil {
			return err
		}
	}

	return txn.Commit()
}

// Always verify that we implement the interface
var _ ac.RelationTupleStore = &SQLStore{}
