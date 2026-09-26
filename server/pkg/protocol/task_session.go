package protocol

// FreshTaskSessionRequest identifies the active claim whose provider context
// the daemon is abandoning before it starts a same-task fresh-session fallback.
type FreshTaskSessionRequest struct {
	RuntimeID    string `json:"runtime_id"`
	DispatchedAt string `json:"dispatched_at"`
}
