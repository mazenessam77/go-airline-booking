package booking

import "context"

// TicketIssuer is an integration boundary. Confirmation alone does not issue a ticket.
// Implementations must use bookingID as an idempotency key and reconcile unknown results.
type TicketIssuer interface {
	Issue(context.Context, string) error
}
