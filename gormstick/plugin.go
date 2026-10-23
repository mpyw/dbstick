// Package gormstick is a GORM plugin that sends the reads of a sticky
// scope to the primary, for a dbstick Tracker. It works with
// gorm.io/plugin/dbresolver, which picks the source or a replica for each
// statement:
//
//	db.Use(dbresolver.Register(...))
//	db.Use(gormstick.New(tracker))
//
// The plugin reads the scope from the statement's context, so pass the
// request's context with db.WithContext.
//
// It does not record writes. Open the sources with a driver hook that
// does: github.com/mpyw/dbstick/pgxstick for PostgreSQL through pgx, or
// github.com/mpyw/dbstick/sqlstick for any other driver.
package gormstick

import (
	"context"
	"errors"

	"gorm.io/gorm"
	"gorm.io/plugin/dbresolver"
)

// Checker reports whether a scope is sticky. *dbstick.Tracker of
// github.com/mpyw/dbstick is one. This package does not import dbstick,
// so its versions do not depend on dbstick's.
type Checker interface {
	IsSticky(ctx context.Context) bool
}

// resolverCallback is the name of dbresolver's callbacks.
const resolverCallback = "gorm:db_resolver"

// Plugin sends the reads of a sticky scope to dbresolver's sources.
type Plugin struct {
	checker Checker
}

// New returns a Plugin that asks checker, such as a *dbstick.Tracker,
// whether a scope is sticky. It panics if checker is nil.
func New(checker Checker) *Plugin {
	if checker == nil {
		panic("gormstick: New: checker is nil")
	}
	return &Plugin{checker: checker}
}

// Name returns "dbstick".
func (p *Plugin) Name() string {
	return "dbstick"
}

// Initialize registers a callback that runs before each query, row and
// raw statement. It fails if dbresolver is not registered yet.
func (p *Plugin) Initialize(db *gorm.DB) error {
	if db.Callback().Query().Get(resolverCallback) == nil {
		return errors.New("gormstick: register gorm.io/plugin/dbresolver before this plugin")
	}
	return errors.Join(
		db.Callback().Query().Before("gorm:query").Register("dbstick:stick", p.stick),
		db.Callback().Row().Before("gorm:row").Register("dbstick:stick", p.stick),
		db.Callback().Raw().Before("gorm:raw").Register("dbstick:stick", p.stick),
	)
}

// stick marks the statement as a write if its scope is sticky, as
// db.Clauses(dbresolver.Write) does. That runs dbresolver again, so the
// order of the callbacks does not matter. In a transaction, dbresolver
// keeps the transaction's connection.
func (p *Plugin) stick(db *gorm.DB) {
	if p.checker.IsSticky(db.Statement.Context) {
		dbresolver.Write.ModifyStatement(db.Statement)
	}
}
