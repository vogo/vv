package dispatches

import (
	"errors"
	"io"
	"testing"

	"github.com/vogo/vage/schema"
)

// collectEvents drains a RunStream into a slice. Test helper retained from
// the M5 stats_integration_test.go file (the rest of which was deleted in
// M6 G2 because it exercised explorer/planner phase events that no
// longer exist).
func collectEvents(t *testing.T, stream *schema.RunStream) []schema.Event {
	t.Helper()

	var events []schema.Event

	for {
		ev, recvErr := stream.Recv()
		if recvErr != nil {
			if errors.Is(recvErr, io.EOF) {
				break
			}

			t.Fatalf("stream.Recv: %v", recvErr)
		}

		events = append(events, ev)
	}

	return events
}
