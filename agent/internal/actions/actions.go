// Package actions defines the explicit, named operations the agent performs.
//
// The agent never executes arbitrary commands. Every capability is an Action
// with a fixed name, input validation and a structured result. Actions that
// change the server are marked Mutating and are refused unless write actions
// are enabled in the configuration.
package actions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"time"
)

// Handler executes an action with raw JSON parameters.
type Handler func(ctx context.Context, params json.RawMessage) (any, error)

// Action is a named operation.
type Action struct {
	Name        string
	Description string
	Mutating    bool
	Handler     Handler
	// Timeout overrides the default action timeout (e.g. certificate
	// issuance); zero uses the default.
	Timeout time.Duration
}

// Error is a user-safe failure with a stable code.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	// Details holds redacted technical output (e.g. from nginx -t) that
	// helps an operator; it never contains stack traces.
	Details string `json:"details,omitempty"`
	Status  int    `json:"-"`
}

// Failed is an operational failure that is safe to explain to the user.
func Failed(msg, details string) *Error {
	return &Error{Code: "failed", Message: msg, Details: details, Status: http.StatusConflict}
}

// Conflict reports that the request collides with existing state.
func Conflict(msg string) *Error {
	return &Error{Code: "conflict", Message: msg, Status: http.StatusConflict}
}

// NotFound reports a missing object.
func NotFound(msg string) *Error {
	return &Error{Code: "not_found", Message: msg, Status: http.StatusNotFound}
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

// Format prints the details too with %+v (tests and logs; never sent to
// the panel as part of the message).
func (e *Error) Format(f fmt.State, verb rune) {
	s := e.Error()
	if verb == 'v' && f.Flag('+') && e.Details != "" {
		s += "\n" + e.Details
	}
	fmt.Fprint(f, s)
}

// Common errors.
var (
	ErrUnknownAction = &Error{Code: "unknown_action", Message: "The requested action does not exist.", Status: http.StatusNotFound}
	ErrWriteDisabled = &Error{Code: "write_disabled", Message: "This agent is running in read-only mode; modifying actions are disabled.", Status: http.StatusForbidden}
)

// InvalidParams returns a validation error.
func InvalidParams(msg string) *Error {
	return &Error{Code: "invalid_params", Message: msg, Status: http.StatusUnprocessableEntity}
}

// Internal wraps an unexpected failure. The detail is logged by the caller
// but never returned to the client.
func Internal() *Error {
	return &Error{Code: "internal_error", Message: "The agent could not complete the action. See the agent log for details.", Status: http.StatusInternalServerError}
}

// AsError converts err into an *Error (Internal if it is not one).
func AsError(err error) *Error {
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	return Internal()
}

var validName = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)*$`)

// Registry holds the available actions.
type Registry struct {
	actions    map[string]Action
	allowWrite bool
}

// NewRegistry creates an empty registry.
func NewRegistry(allowWrite bool) *Registry {
	return &Registry{actions: map[string]Action{}, allowWrite: allowWrite}
}

// Register adds an action. It panics on programmer errors (bad or duplicate
// names) so they surface at start-up.
func (r *Registry) Register(a Action) {
	if !validName.MatchString(a.Name) {
		panic(fmt.Sprintf("invalid action name %q", a.Name))
	}
	if _, dup := r.actions[a.Name]; dup {
		panic(fmt.Sprintf("duplicate action %q", a.Name))
	}
	if a.Handler == nil {
		panic(fmt.Sprintf("action %q has no handler", a.Name))
	}
	r.actions[a.Name] = a
}

// Lookup finds an action and enforces the write gate.
func (r *Registry) Lookup(name string) (Action, error) {
	a, ok := r.actions[name]
	if !ok {
		return Action{}, ErrUnknownAction
	}
	if a.Mutating && !r.allowWrite {
		return Action{}, ErrWriteDisabled
	}
	return a, nil
}

// Info describes an action for discovery by clients.
type Info struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Mutating    bool   `json:"mutating"`
	Enabled     bool   `json:"enabled"`
}

// List returns all registered actions sorted by name.
func (r *Registry) List() []Info {
	out := make([]Info, 0, len(r.actions))
	for _, a := range r.actions {
		out = append(out, Info{Name: a.Name, Description: a.Description, Mutating: a.Mutating, Enabled: !a.Mutating || r.allowWrite})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// DecodeParams strictly decodes params into v. Empty params decode to the
// zero value.
func DecodeParams(params json.RawMessage, v any) error {
	if len(params) == 0 || string(params) == "null" {
		return nil
	}
	dec := json.NewDecoder(bytesReader(params))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return InvalidParams("Parameters are not valid for this action.")
	}
	return nil
}
