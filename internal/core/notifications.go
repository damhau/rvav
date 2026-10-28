package core

import (
	"context"
	"errors"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/store"
)

// Notifications (ADR-0023): deciding from a Teams or Slack card.

type channelKey struct{}

// fromChannel is the channel a decision came from, for the audit log.
func fromChannel(ctx context.Context) string {
	ch, _ := ctx.Value(channelKey{}).(string)
	return ch
}

// Why a decision from a card wasn't taken, for the card's answer.
var (
	ErrCardNotYours = errors.New("not_yours")
	ErrCardDecided  = errors.New("decided")
	ErrCardComment  = errors.New("comment")
)

// DecideFromChannel decides an approval from a card on a channel, as the
// user rvav sent it to: externalID must be who rvav knows them as there,
// and the inbox's rules apply. It returns who decided.
func (s *Service) DecideFromChannel(ctx context.Context, channel, externalID, approvalID string, approve bool, comment string) (string, error) {
	userID, err := s.Store.UserOnChannel(ctx, channel, externalID)
	if errors.Is(err, store.ErrNotFound) {
		return "", ErrCardNotYours
	}
	if err != nil {
		return "", err
	}
	sent, err := s.Store.SentApprovalOnChannel(ctx, approvalID, userID, channel)
	if err != nil {
		return "", err
	}
	if !sent {
		return "", ErrCardNotYours
	}
	p, err := auth.PrincipalFor(ctx, s.Store, userID)
	if err != nil {
		return "", err
	}
	d := api.ApprovalDecision{Decision: api.ApprovalDecisionDecisionReject}
	if approve {
		d.Decision = api.ApprovalDecisionDecisionApprove
	}
	if comment != "" {
		d.Comment = &comment
	}
	_, err = s.DecideApproval(context.WithValue(ctx, channelKey{}, channel), p, approvalID, d)
	var conflict *ConflictError
	var invalid *ValidationError
	switch {
	case err == nil:
		return p.User.Name, nil
	case errors.As(err, &conflict):
		return "", ErrCardDecided
	case errors.As(err, &invalid) && !approve && comment == "":
		return "", ErrCardComment
	case errors.Is(err, ErrForbidden), errors.Is(err, ErrNotFound):
		return "", ErrCardNotYours
	}
	return "", err
}
