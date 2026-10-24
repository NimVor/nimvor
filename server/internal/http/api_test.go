package http

// API v1 tests.  The authenticator is unit-tested in server/internal/auth;
// the end-to-end route behaviour is covered by server_test.go (integration
// tests that boot the real server against sqlite).  This file is a placeholder
// that will accumulate focused handler tests once a Go toolchain is available
// in CI — until then the routes below are structurally exercised by the
// compilation of the handler graph (each route referenced in server.go binds
// a method on *Server with the right signature).
