package mock

// Options configures one of the NewXService handlers.
type Options struct {
	// World is the shared state the handler reads and mutates. Required;
	// pass the same World to all three NewXService constructors so
	// cross-service effects (allocate, cancel, release) are visible to
	// every service immediately.
	World *World

	// RequireAuth, if true, rejects requests without a well-formed
	// "Authorization: Bearer <token>" header with 401 UNAUTHORIZED.
	// Default: off.
	RequireAuth bool
}
