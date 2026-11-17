package main

import (
	"errors"
	"fmt"
	"io"

	"github.com/centre-for-dpi/data-exchange-provider/internal/i18n"
	"github.com/centre-for-dpi/data-exchange-provider/internal/state"
)

// identity tells the installer whether the node is enrolled: 0 and the node's name, 3 without
// state (never enrolled, or the enrollment failed), 1 if the state cannot be read.
func identity(stateDir string, out io.Writer) int {
	st, err := (state.Store{Dir: stateDir}).Load()
	switch {
	case errors.Is(err, state.ErrNoState):
		i18n.Fprintln(out, "no identity: the node is not enrolled")
		return 3
	case err != nil:
		fmt.Fprintln(out, err)
		return 1
	}
	fmt.Fprintln(out, st.NodeName)
	return 0
}

// importState copies a backed-up state (a directory with state.json) into the node's state
// directory, after checking it reads as a state. Used by `install.sh restore`.
func importState(src, dst string, out io.Writer) int {
	st, err := (state.Store{Dir: src}).Load()
	if err != nil {
		i18n.Fprintf(out, "the backup has no readable node state in %s: %v\n", src, err)
		return 1
	}
	if err := (state.Store{Dir: dst}).Save(st); err != nil {
		i18n.Fprintf(out, "could not write the state to %s: %v\n", dst, err)
		return 1
	}
	i18n.Fprintf(out, "state of %s restored\n", st.NodeName)
	return 0
}
