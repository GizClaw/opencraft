package host

import (
	"context"
)

// ForkConversation creates a new session whose transcript contains the
// source conversation through sourceRunID, and copies attachment files
// and settings.
//
// There is nothing to seed: the model's history is a projection of the
// transcript, and the fork's transcript is the source's prefix by
// construction, so the next turn continues with exactly the context the
// source had at that point.
func (h *Host) ForkConversation(
	ctx context.Context, sourceID, sourceRunID string,
) (string, error) {
	if h == nil || h.store == nil {
		return "", ErrSessionStoreNotReady
	}
	forked, err := h.store.Fork(ctx, sourceID, sourceRunID)
	if err != nil {
		return "", err
	}
	return forked.ID, nil
}
