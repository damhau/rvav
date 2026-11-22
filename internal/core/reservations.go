package core

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5"

	"github.com/damhau/rvav/internal/store"
)

// placementTries is how many times a request is placed before it gives up
// on clusters others keep filling.
const placementTries = 5

// errPlacementMoved: requests placed at the same time took room on a
// cluster the strategy chose counting on it.
var errPlacementMoved = errors.New("placement moved")

// reservedSeen is what other requests' VMs not built yet held on a
// cluster ("vcenter/cluster") when a strategy chose it.
type reservedSeen struct {
	key string
	mb  int64
	vms int
}

// withReservations counts in placement what other requests' VMs not built
// yet hold (ADR-0036); except is the request evaluated again, whose own
// don't count.
func (s *Service) withReservations(ctx context.Context, pl *placement, except string) error {
	var err error
	pl.reserved, err = s.Store.Reservations(ctx, s.Store.Pool, except)
	return err
}

// chose records what others held on the cluster the strategy chose.
func (p *vmPart) chose(pl *placement, c *store.InvCluster) {
	mb, vms := pl.reservedOn(c, "")
	p.seen = &reservedSeen{key: c.VCenter + "/" + c.Name, mb: mb, vms: vms}
}

// seenOn is, for each cluster a strategy chose, the least others held
// there when it chose it.
func (ev *Evaluation) seenOn() map[string]reservedSeen {
	out := map[string]reservedSeen{}
	for _, p := range ev.allParts() {
		if p.seen == nil || p.count == 0 {
			continue
		}
		if was, ok := out[p.seen.key]; ok {
			out[p.seen.key] = reservedSeen{key: was.key, mb: min(was.mb, p.seen.mb), vms: min(was.vms, p.seen.vms)}
		} else {
			out[p.seen.key] = *p.seen
		}
	}
	return out
}

// holdPlacement reserves the room of a request's VMs not built yet
// (ADR-0036). It holds their clusters until the transaction ends, so that
// two requests placed at the same time can't both count on the same room:
// with ev, it is errPlacementMoved when others took some on a cluster
// ev's strategy chose since.
func (s *Service) holdPlacement(ctx context.Context, tx pgx.Tx, ev *Evaluation, requestID string, rows []store.Reservation) error {
	var seen map[string]reservedSeen
	if ev != nil {
		seen = ev.seenOn()
	}
	return s.holdSeen(ctx, tx, seen, requestID, rows)
}

// holdSeen is holdPlacement given what others held on each cluster when
// it was chosen.
func (s *Service) holdSeen(ctx context.Context, tx pgx.Tx, seen map[string]reservedSeen, requestID string, rows []store.Reservation) error {
	keys, err := s.lockRows(ctx, tx, rows)
	if err != nil {
		return err
	}
	if err := s.placedStill(ctx, tx, seen, keys, requestID); err != nil {
		return err
	}
	return s.Store.Reserve(ctx, tx, requestID, rows)
}

func (s *Service) lockRows(ctx context.Context, tx pgx.Tx, rows []store.Reservation) ([]string, error) {
	var keys []string
	for _, r := range rows {
		keys = append(keys, r.VCenter+"/"+r.Cluster)
	}
	return keys, s.Store.LockClusters(ctx, tx, keys)
}

// holdRoomAgain reserves again the room of a request's VMs where they were
// placed, as a retry does. Placing never refuses a cluster short of memory,
// so it is a ConflictError only when what other requests reserved since
// took the room its VMs not built yet need there.
func (s *Service) holdRoomAgain(ctx context.Context, tx pgx.Tx, pl *placement, r store.RequestRow, rows []store.Reservation) error {
	if _, err := s.lockRows(ctx, tx, rows); err != nil {
		return err
	}
	if err := s.Store.Reserve(ctx, tx, r.ID, rows); err != nil || len(rows) == 0 {
		return err
	}
	// Those built are in their cluster's memory used already.
	own, err := s.Store.ReservationsOf(ctx, tx, r.ID)
	if err != nil {
		return err
	}
	need := map[string]int64{}
	var order []string
	for _, x := range own {
		k := x.VCenter + "/" + x.Cluster
		if _, ok := need[k]; !ok {
			order = append(order, k)
		}
		need[k] += x.MemoryMB
	}
	others, err := s.Store.Reservations(ctx, tx, r.ID)
	if err != nil {
		return err
	}
	for _, k := range order {
		i := slices.IndexFunc(pl.all, func(c store.InvCluster) bool { return c.VCenter+"/"+c.Name == k })
		if i < 0 {
			continue
		}
		pl.reserved = nil
		bare := pl.freeOn(&pl.all[i], "")
		pl.reserved = others
		if free := pl.freeOn(&pl.all[i], ""); free < need[k] && bare >= need[k] {
			return &ConflictError{Msg: fmt.Sprintf("%s has %d MB of memory free, counting what other requests reserved; "+
				"this request's VMs not built yet need %d MB there. Retry once it has room, or request the VMs again.",
				pl.all[i].Name, max(free, 0), need[k])}
		}
	}
	return nil
}

// placedStill is errPlacementMoved when others hold more on one of these
// clusters than when the strategy chose it.
func (s *Service) placedStill(ctx context.Context, tx pgx.Tx, seen map[string]reservedSeen, keys []string, requestID string) error {
	if len(seen) == 0 {
		return nil
	}
	now, err := s.Store.Reservations(ctx, tx, requestID)
	if err != nil {
		return err
	}
	for key, was := range seen {
		if !slices.Contains(keys, key) {
			continue
		}
		var mb int64
		var vms int
		for _, r := range now {
			if r.VCenter+"/"+r.Cluster == key {
				mb, vms = mb+r.MemoryMB, vms+r.VMs
			}
		}
		if mb > was.mb || vms > was.vms {
			s.Log.Info("placing again: requests placed at the same time took room on the cluster chosen",
				"cluster", key, "reserved_mb", mb, "counted_mb", was.mb)
			return errPlacementMoved
		}
	}
	return nil
}

// placing places a request, its evaluation and transaction, again while
// others placed at the same time took the room it counted on: from the
// second time, one at a time, so that each counts what those before it
// reserved.
func (s *Service) placing(ctx context.Context, place func() (store.RequestRow, error)) (store.RequestRow, error) {
	var out store.RequestRow
	err := s.placeAgain(ctx, func() error {
		var err error
		out, err = place()
		return err
	})
	if errors.Is(err, errPlacementMoved) {
		return store.RequestRow{}, &ConflictError{Msg: "Requests placed at the same time keep taking the room chosen for these VMs. Try again.",
			Retry: true}
	}
	return out, err
}

// placeAgain runs place again, up to placementTries times, while it is
// errPlacementMoved: from the second time, one at a time.
func (s *Service) placeAgain(ctx context.Context, place func() error) error {
	err := place()
	for try := 1; errors.Is(err, errPlacementMoved) && try < placementTries; try++ {
		err = s.Store.WithLock(ctx, store.LockPlacement, place)
	}
	return err
}
