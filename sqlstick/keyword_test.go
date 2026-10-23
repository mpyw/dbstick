package sqlstick

import "testing"

func TestStartsWithWriteKeyword(t *testing.T) {
	for query, want := range map[string]bool{
		"INSERT INTO t VALUES (1) RETURNING id": true,
		"update t SET a = 1":                    true,
		"Delete FROM t":                         true,
		"MERGE INTO t USING s ON true":          true,
		"REPLACE INTO t VALUES (1)":             true,
		"  \n\t(INSERT INTO t VALUES (1))":      true,
		"-- note\nINSERT INTO t VALUES (1)":     true,
		"# note\nDELETE FROM t":                 true,
		"/* note */ UPDATE t SET a = 1":         true,
		"/* a */ -- b\n/* c */INSERT":           true,
		"INSERT":                                true,
		"SELECT * FROM t":                       false,
		"WITH x AS (DELETE FROM t) SELECT 1":    false,
		"INSERTED":                              false,
		"-- only a comment":                     false,
		"/* unclosed INSERT":                    false,
		"":                                      false,
	} {
		if got := startsWithWriteKeyword(query); got != want {
			t.Errorf("startsWithWriteKeyword(%q) = %v, want %v", query, got, want)
		}
	}
}
