package sdata_test

import (
	"testing"

	"github.com/aegion-dynamic/graphjin-slim/core/v3/sdata"
)

// TestCompositeFKIsOneRelation pins the shape of a composite foreign key.
//
// A composite foreign key is one relation that joins on all of its columns.
// It is not two relations, one for each column: two relations make the
// nested selection ambiguous, and a nested selection of a child of a
// composite key has to come back as a list.
func TestCompositeFKIsOneRelation(t *testing.T) {
	di := GetTestCompositeFKDBInfo()
	schema, err := NewDBSchema(di, nil)
	if err != nil {
		t.Fatal(err)
	}

	// The child to the parent: one relation, joined on both columns.
	path, err := schema.FindPath("enrollment", "course_offering", "")
	if err != nil {
		t.Fatalf("FindPath(enrollment, course_offering): %v", err)
	}
	rel := sdata.PathToRel(path[0])
	if rel.Type != sdata.RelOneToOne {
		t.Errorf("child to parent relation = %v, want one to one", rel.Type)
	}

	// The parent to the child: a list, because a parent has many children.
	path, err = schema.FindPath("course_offering", "enrollment", "")
	if err != nil {
		t.Fatalf("FindPath(course_offering, enrollment): %v", err)
	}
	rel = sdata.PathToRel(path[0])
	if rel.Type != sdata.RelOneToMany {
		t.Errorf("parent to child relation = %v, want one to many", rel.Type)
	}

	// The relation joins on both columns of the composite key.
	if len(rel.ExtraPairs) != 1 {
		t.Fatalf("the relation has %d extra column pairs, want 1", len(rel.ExtraPairs))
	}
	pair := rel.ExtraPairs[0]
	if pair.L.Name != "course_id" || pair.R.Name != "course_id" {
		t.Errorf("the extra pair is %s -> %s, want course_id -> course_id",
			pair.L.Name, pair.R.Name)
	}
}

// TestCompositeFKOnAUniqueKey pins the cardinality of a composite foreign key
// whose tuple is a unique key and not the primary key.
//
// The tuple is unique, so the child belongs to one parent and the parent has
// many children. The columns are not unique on their own, so the relation
// cannot be decided by one column's flag: which column the key is read from
// must not change the answer.
func TestCompositeFKOnAUniqueKey(t *testing.T) {
	di := GetTestCompositeFKDBInfo()
	// The parent's key is a unique key, not its primary key, and its columns
	// are not unique on their own.
	di.Tables[0].PrimaryCols = nil
	for i := range di.Tables[0].Columns {
		di.Tables[0].Columns[i].UniqueKey = false
	}
	di.UniqueKeys = []sdata.UniqueKeyInfo{
		{
			Schema:  "public",
			Table:   "course_offering",
			Columns: []string{"term_id", "course_id"},
		},
	}

	schema, err := NewDBSchema(di, nil)
	if err != nil {
		t.Fatal(err)
	}

	path, err := schema.FindPath("enrollment", "course_offering", "")
	if err != nil {
		t.Fatalf("FindPath(enrollment, course_offering): %v", err)
	}
	rel := sdata.PathToRel(path[0])
	if rel.Type != sdata.RelOneToOne {
		t.Errorf("child to parent relation = %v, want one to one", rel.Type)
	}

	path, err = schema.FindPath("course_offering", "enrollment", "")
	if err != nil {
		t.Fatalf("FindPath(course_offering, enrollment): %v", err)
	}
	rel = sdata.PathToRel(path[0])
	if rel.Type != sdata.RelOneToMany {
		t.Errorf("parent to child relation = %v, want one to many", rel.Type)
	}
}
