package db

import "testing"

func TestSqliteDSN(t *testing.T) {
	got := sqliteDSN("data/gateway.db")
	want := "file:data/gateway.db?_journal_mode=WAL&_busy_timeout=5000&_synchronous=NORMAL&cache=shared"
	if got != want {
		t.Fatalf("got %s", got)
	}
	passthrough := "file:foo.db?mode=ro"
	if sqliteDSN(passthrough) != passthrough {
		t.Fatalf("should keep explicit dsn")
	}
}
