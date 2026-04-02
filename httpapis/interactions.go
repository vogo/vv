package httpapis

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"
)

// Interaction represents a pending user interaction requested by an agent.
type Interaction struct {
	ID        string    `json:"id"`
	Question  string    `json:"question"`
	Response  string    `json:"response,omitempty"`
	Responded bool      `json:"responded"`
	CreatedAt time.Time `json:"created_at"`
	done      chan struct{}
}

// InteractionStore manages pending user interactions.
type InteractionStore struct {
	mu           sync.Mutex
	interactions map[string]*Interaction
	timeout      time.Duration
}

// NewInteractionStore creates an InteractionStore with the given timeout.
// A background cleanup goroutine runs until the context is canceled.
func NewInteractionStore(ctx context.Context, timeout time.Duration) *InteractionStore {
	s := &InteractionStore{
		interactions: make(map[string]*Interaction),
		timeout:      timeout,
	}

	go s.cleanup(ctx)

	return s
}

// Create creates a new interaction with the given question and returns it.
func (s *InteractionStore) Create(question string) *Interaction {
	id := generateID()
	interaction := &Interaction{
		ID:        id,
		Question:  question,
		CreatedAt: time.Now(),
		done:      make(chan struct{}),
	}

	s.mu.Lock()
	s.interactions[id] = interaction
	s.mu.Unlock()

	return interaction
}

// Respond submits a response to a pending interaction.
// Returns an error if the interaction is not found or already responded.
func (s *InteractionStore) Respond(id, response string) error {
	s.mu.Lock()
	interaction, ok := s.interactions[id]
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("interaction %q not found", id)
	}

	if interaction.Responded {
		s.mu.Unlock()
		return fmt.Errorf("interaction %q already responded", id)
	}

	interaction.Response = response
	interaction.Responded = true
	s.mu.Unlock()

	close(interaction.done)

	return nil
}

// Get retrieves an interaction by ID.
func (s *InteractionStore) Get(id string) (*Interaction, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	interaction, ok := s.interactions[id]

	return interaction, ok
}

// cleanup removes expired interactions periodically.
func (s *InteractionStore) cleanup(ctx context.Context) {
	ticker := time.NewTicker(s.timeout)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.mu.Lock()
			cutoff := time.Now().Add(-2 * s.timeout)

			for id, interaction := range s.interactions {
				if interaction.CreatedAt.Before(cutoff) {
					delete(s.interactions, id)
				}
			}

			s.mu.Unlock()
		}
	}
}

func generateID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)

	return hex.EncodeToString(b)
}
