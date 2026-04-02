package httpapis

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/vogo/vage/schema"
)

func TestInteractionStore_CreateAndRespond(t *testing.T) {
	ctx := t.Context()

	store := NewInteractionStore(ctx, 5*time.Minute)
	interaction := store.Create("What database?")

	if interaction.ID == "" {
		t.Fatal("expected non-empty interaction ID")
	}

	if interaction.Question != "What database?" {
		t.Errorf("Question = %q, want %q", interaction.Question, "What database?")
	}

	if interaction.Responded {
		t.Error("expected Responded to be false initially")
	}

	err := store.Respond(interaction.ID, "PostgreSQL")
	if err != nil {
		t.Fatalf("Respond: %v", err)
	}

	if !interaction.Responded {
		t.Error("expected Responded to be true after responding")
	}

	if interaction.Response != "PostgreSQL" {
		t.Errorf("Response = %q, want %q", interaction.Response, "PostgreSQL")
	}

	// Verify done channel is closed.
	select {
	case <-interaction.done:
		// OK, channel is closed.
	default:
		t.Error("expected done channel to be closed after responding")
	}
}

func TestInteractionStore_DoubleRespond(t *testing.T) {
	ctx := t.Context()

	store := NewInteractionStore(ctx, 5*time.Minute)
	interaction := store.Create("What database?")

	err := store.Respond(interaction.ID, "PostgreSQL")
	if err != nil {
		t.Fatalf("first Respond: %v", err)
	}

	err = store.Respond(interaction.ID, "MySQL")
	if err == nil {
		t.Fatal("expected error for double respond")
	}

	if !strings.Contains(err.Error(), "already responded") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestInteractionStore_RespondNotFound(t *testing.T) {
	ctx := t.Context()

	store := NewInteractionStore(ctx, 5*time.Minute)

	err := store.Respond("nonexistent", "response")
	if err == nil {
		t.Fatal("expected error for nonexistent interaction")
	}

	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestInteractionStore_Get(t *testing.T) {
	ctx := t.Context()

	store := NewInteractionStore(ctx, 5*time.Minute)
	interaction := store.Create("What database?")

	got, ok := store.Get(interaction.ID)
	if !ok {
		t.Fatal("expected interaction to be found")
	}

	if got.ID != interaction.ID {
		t.Errorf("ID = %q, want %q", got.ID, interaction.ID)
	}
}

func TestInteractionStore_GetNotFound(t *testing.T) {
	ctx := t.Context()

	store := NewInteractionStore(ctx, 5*time.Minute)

	_, ok := store.Get("nonexistent")
	if ok {
		t.Error("expected interaction to not be found")
	}
}

func TestInteractionStore_Cleanup(t *testing.T) {
	ctx := t.Context()

	// Use a very short timeout so cleanup runs quickly.
	store := NewInteractionStore(ctx, 50*time.Millisecond)
	interaction := store.Create("old question")

	// Artificially age the interaction.
	store.mu.Lock()
	interaction.CreatedAt = time.Now().Add(-5 * time.Minute)
	store.mu.Unlock()

	// Wait for cleanup to run.
	time.Sleep(150 * time.Millisecond)

	_, ok := store.Get(interaction.ID)
	if ok {
		t.Error("expected expired interaction to be cleaned up")
	}
}

func TestHTTPInteractor_AskUser(t *testing.T) {
	ctx := t.Context()

	store := NewInteractionStore(ctx, 5*time.Minute)

	var emittedEvent bool

	interactor := NewHTTPInteractor(store, func(_ schema.Event) {
		emittedEvent = true
	})

	// Respond asynchronously.
	go func() {
		// Wait for the interaction to be created.
		time.Sleep(50 * time.Millisecond)

		store.mu.Lock()
		var id string
		for k := range store.interactions {
			id = k
			break
		}
		store.mu.Unlock()

		if id != "" {
			_ = store.Respond(id, "Use PostgreSQL")
		}
	}()

	response, err := interactor.AskUser(ctx, "Which database?")
	if err != nil {
		t.Fatalf("AskUser: %v", err)
	}

	if response != "Use PostgreSQL" {
		t.Errorf("response = %q, want %q", response, "Use PostgreSQL")
	}

	if !emittedEvent {
		t.Error("expected event to be emitted")
	}
}

func TestHTTPInteractor_AskUser_Timeout(t *testing.T) {
	storeCtx := t.Context()

	store := NewInteractionStore(storeCtx, 5*time.Minute)
	interactor := NewHTTPInteractor(store, nil)

	// Use a very short timeout for the ask context.
	askCtx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	response, err := interactor.AskUser(askCtx, "Will you answer?")
	if err != nil {
		t.Fatalf("AskUser: %v", err)
	}

	if !strings.Contains(response, "best judgment") {
		t.Errorf("expected timeout fallback message, got: %s", response)
	}
}
