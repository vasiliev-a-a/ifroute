package ifroute

import "fmt"

type RouteError struct {
	op     string
	prefix string
	ifName string
	err    error
}

func (err *RouteError) Error() string {
	return fmt.Sprintf("%s(prefix=%s, ifName=%s): %v", err.op, err.prefix, err.ifName, err.err)
}

func (err *RouteError) Unwrap() error { return err.err }
