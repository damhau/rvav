package snowsync

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/damhau/rvav/internal/integrations/servicenow"
	"github.com/damhau/rvav/internal/store"
)

// notesRounds bounds how many notes of one requested item a pass sends:
// each round sends the next of each.
const notesRounds = 10

// notes sends the work notes owed to requested items, in order: a note
// ServiceNow refuses waits with backoff, and those after it wait too.
func (s *Syncer) notes(ctx context.Context, cl *servicenow.Client) error {
	for range notesRounds {
		due, err := s.Store.DueServiceNowNotes(ctx, s.now(), batch)
		if err != nil || len(due) == 0 {
			return err
		}
		sent := 0
		for _, n := range due {
			state, text := Note(n, s.PublicURL)
			if text != "" {
				values := map[string]string{"work_notes": text}
				if state != "" {
					values["state"] = state
				}
				err := cl.Update(ctx, servicenow.TableRequestedItem, n.RITMSysID, values)
				switch {
				case servicenow.IsNotFound(err):
					s.log().Warn("servicenow work note: the requested item is gone", "ritm", n.RITMNumber)
				case err != nil:
					if err := s.Store.RetryServiceNowNote(ctx, n.ID, s.now().Add(backoff(n.Attempts)), err.Error()); err != nil {
						return err
					}
					continue
				}
			}
			if err := s.Store.SentServiceNowNote(ctx, n.ID, s.now()); err != nil {
				return err
			}
			sent++
		}
		if sent == 0 {
			return nil
		}
	}
	return nil
}

// event is what a work note reads of an event (rvav.event.v1).
type event struct {
	Data struct {
		Request struct {
			Number  string  `json:"number"`
			Status  string  `json:"status"`
			Outcome *string `json:"outcome"`
		} `json:"request"`
		Item struct {
			Name string `json:"name"`
		} `json:"item"`
		Deployments []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"deployments"`
		Approval *struct {
			Name      string  `json:"name"`
			Level     int     `json:"level"`
			Outcome   *string `json:"outcome"`
			Approvers []struct {
				Name string `json:"name"`
			} `json:"approvers"`
			Groups    []string `json:"groups"`
			DecidedBy *struct {
				Name string `json:"name"`
			} `json:"decided_by"`
		} `json:"approval"`
	} `json:"data"`
}

// Note is the work note an event of a request writes on its requested
// item, and the item's state after it ("" leaves it): work in progress
// while rvav works, closed complete when the request succeeded, closed
// incomplete when it failed, was rejected or cancelled. "" for an event
// that says nothing to ServiceNow.
func Note(n store.ServiceNowNote, publicURL string) (state, text string) {
	var ev event
	if json.Unmarshal(n.Payload, &ev) != nil {
		return "", ""
	}
	r := ev.Data.Request
	link := func(path string) string {
		if publicURL == "" {
			return ""
		}
		return " " + strings.TrimSuffix(publicURL, "/") + path
	}
	switch n.Topic {
	case "request.submitted":
		return servicenow.ItemWorkInProgress, fmt.Sprintf("rvav: request %s (%s) submitted.%s", r.Number, ev.Data.Item.Name,
			link("/requests/"+r.Number))
	case "approval.requested":
		a := ev.Data.Approval
		if a == nil {
			return "", ""
		}
		var who []string
		for _, p := range a.Approvers {
			who = append(who, p.Name)
		}
		who = append(who, a.Groups...)
		by := ""
		if len(who) > 0 {
			by = ", by " + strings.Join(who, ", ")
		}
		return "", fmt.Sprintf("rvav: %s waits for approval: %s (level %d)%s.", r.Number, a.Name, a.Level, by)
	case "approval.decided":
		a := ev.Data.Approval
		if a == nil || a.Outcome == nil {
			return "", ""
		}
		by := ""
		if a.DecidedBy != nil {
			by = " by " + a.DecidedBy.Name
		}
		return "", fmt.Sprintf("rvav: %s %s%s at %s (level %d).", r.Number, *a.Outcome, by, a.Name, a.Level)
	case "provisioning.post":
		outcome := ""
		if r.Outcome != nil {
			outcome = *r.Outcome
		}
		switch {
		case outcome == "succeeded":
			var deps []string
			for _, d := range ev.Data.Deployments {
				deps = append(deps, d.Name+link("/deployments/"+d.ID))
			}
			return servicenow.ItemClosedComplete, fmt.Sprintf("rvav: %s succeeded. Deployment: %s", r.Number, strings.Join(deps, "; "))
		case r.Status == "rejected":
			return servicenow.ItemClosedIncomplete, fmt.Sprintf("rvav: %s was rejected.%s", r.Number, link("/requests/"+r.Number))
		case outcome == "cancelled":
			return servicenow.ItemClosedIncomplete, fmt.Sprintf("rvav: %s was cancelled.%s", r.Number, link("/requests/"+r.Number))
		default:
			return servicenow.ItemClosedIncomplete, fmt.Sprintf("rvav: %s failed.%s", r.Number, link("/requests/"+r.Number))
		}
	}
	return "", ""
}
