package main

import (
	"duckfam.us/sngl/internal/testsql"
	"rsc.io/script"
)

// sqlserverCmd starts the harness's server for an engine, `postgresql` or
// `mysql`, and makes each database named a new, empty one, which a program
// opens at testsql.Source. A script guards it with the engine's condition,
// since a launch answered from its record reaches no server.
func sqlserverCmd() script.Cmd {
	return script.Command(
		script.CmdUsage{
			Summary: "start the test PostgreSQL or MySQL server and empty databases on it",
			Args:    "engine dbs...",
		},
		func(s *script.State, args ...string) (script.WaitFunc, error) {
			if len(args) < 2 {
				return nil, script.ErrUsage
			}
			return nil, testsql.Fresh(args[0], args[1:]...)
		},
	)
}
