package httpapis

import (
	"context"

	"github.com/vogo/vage/schema"
)

// HTTPInteractor implements askuser.UserInteractor for HTTP mode.
// It stores a pending interaction and emits a pending_interaction SSE event
// via the constructor-injected callback.
type HTTPInteractor struct {
	store  *InteractionStore
	emitFn func(schema.Event) // emit SSE event; set at construction time
}

// NewHTTPInteractor creates an HTTPInteractor with the given store and event emitter.
func NewHTTPInteractor(store *InteractionStore, emitFn func(schema.Event)) *HTTPInteractor {
	return &HTTPInteractor{store: store, emitFn: emitFn}
}

// AskUser creates a pending interaction, emits a pending_interaction event,
// and blocks until the user responds or the context is canceled.
func (h *HTTPInteractor) AskUser(ctx context.Context, question string) (string, error) {
	interaction := h.store.Create(question)

	// Emit pending_interaction event via the constructor-injected callback.
	if h.emitFn != nil {
		h.emitFn(schema.NewEvent(schema.EventPendingInteraction, "", "", schema.PendingInteractionData{
			InteractionID:  interaction.ID,
			Question:       question,
			TimeoutSeconds: int(h.store.timeout.Seconds()),
		}))
	}

	select {
	case <-interaction.done:
		return interaction.Response, nil
	case <-ctx.Done():
		return "User did not respond within the timeout. " +
			"Proceed with your best judgment.", nil
	}
}
