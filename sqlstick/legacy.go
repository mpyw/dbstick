package sqlstick

import (
	"context"
	"database/sql/driver"
	"errors"
)

// callLegacy calls a method of a driver that takes no context, such as
// driver.Execer's Exec, as database/sql does. It checks ctx only before
// the call, since the method cannot be canceled.
//
//declscope:shared // wrap.go calls a driver with no context through it
func callLegacy[R any](ctx context.Context, args []driver.NamedValue, method func([]driver.Value) (R, error)) (R, error) {
	values, err := legacyValues(args)
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		var zero R
		return zero, err
	}
	return method(values)
}

// legacyValues returns the values of args, which a method with no context
// takes. Such a method takes no names, so a named argument is an error,
// as in database/sql.
func legacyValues(args []driver.NamedValue) ([]driver.Value, error) {
	values := make([]driver.Value, len(args))
	for i, arg := range args {
		if arg.Name != "" {
			return nil, errors.New("sql: driver does not support the use of Named Parameters")
		}
		values[i] = arg.Value
	}
	return values, nil
}
