# Development Documentation

## Context Support Refactor

### Overview
To support modern Go best practices and ensure that long-running operations (like Twitch API calls) can be correctly cancelled or timed out, `context.Context` has been propagated through the entire service stack.

### Implementation Details

#### 1. Service Layer (`internal/service`)
All major methods that perform external I/O or intensive processing now accept a `context.Context` argument.
- `SetUserWatchStateContext`
- `ResolveDisplayNameContext`
- `ListFeedContext`

#### 2. Repository Layer (`internal/repository`)
The `Repository` interface was updated to allow context propagation when needed, particularly for operations involving external enrichment.

#### 3. API Layer (`main.go` & `internal/server`)
The HTTP handlers now correctly pass the request context (`r.Context()`) down to the service layer. This ensures that if a user closes their browser or the connection is lost, the backend work is cancelled appropriately.

### Benefits
- **Resource Management**: Prevents leaking goroutines when requests are abandoned.
- **Reliability**: Allows for consistent timeout policies across the application.
- **Observability**: Better integration with tracing and logging tools that rely on context.
