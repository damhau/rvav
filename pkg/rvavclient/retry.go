package rvavclient

import (
	"cmp"
	"context"
	"crypto/tls"
	"errors"
	"net"
	"time"

	"golang.org/x/oauth2"
)

// Transient says whether a call that failed with err is worth making again
// later: it got no answer, or rvav, its identity provider or a proxy in
// front of them answered 408, 429 or 5xx, as during a rolling restart.
func Transient(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, ErrSignInExpired) {
		return false
	}
	var e *Error
	if errors.As(err, &e) {
		return retryable(e.Status)
	}
	var re *oauth2.RetrieveError
	if errors.As(err, &re) {
		return re.Response != nil && retryable(re.Response.StatusCode)
	}
	var cert *tls.CertificateVerificationError
	if errors.As(err, &cert) {
		return false
	}
	var ne net.Error
	return errors.As(err, &ne)
}

func retryable(status int) bool {
	return status == 408 || status == 429 || status >= 500
}

// Retry paces a wait that reads rvav again and again, for example for a
// request to finish: Interval between reads, and after a Transient error a
// pause that doubles from twice Interval up to MaxPause.
type Retry struct {
	Interval time.Duration
	// MaxPause is the longest pause after errors; a minute when zero.
	MaxPause time.Duration
	// Retrying, when set, hears of each error read again after pause.
	Retrying func(err error, pause time.Duration)

	pause time.Duration
}

// Wait pauses before the next read: Interval after one that worked (err
// nil), longer after each Transient error in a row. It returns err when it
// isn't worth reading again, and the context's error once it ends.
func (r *Retry) Wait(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	pause := r.Interval
	switch {
	case err == nil:
		r.pause = 0
	case !Transient(err):
		return err
	default:
		r.pause = min(max(2*r.pause, 2*r.Interval), cmp.Or(r.MaxPause, time.Minute))
		pause = r.pause
		if r.Retrying != nil {
			r.Retrying(err, pause)
		}
	}
	t := time.NewTimer(pause)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
