package dispatch

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/damhau/rvav/internal/integrations/awx"
)

// AWX launches a job template with the event as the extra variable
// rvav_event; the run is delivered when the job succeeds.
type AWX struct {
	Client awx.Client
	// Poll is how often a job is checked; 2 s when zero.
	Poll time.Duration
}

// AWXTarget is a subscription's AWX template.
type AWXTarget struct {
	TemplateID  int    `json:"template_id,omitempty"`
	JobTemplate string `json:"job_template,omitempty"`
}

func (a *AWX) Deliver(ctx context.Context, d Delivery) Result {
	var t struct {
		AWX *AWXTarget `json:"awx"`
	}
	if err := json.Unmarshal(d.Sub.Target, &t); err != nil || t.AWX == nil {
		return Result{Err: "not an AWX template"}
	}
	timeout := time.Duration(d.Sub.TimeoutSeconds) * time.Second
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// A job an earlier attempt launched is followed, not launched again.
	jobID := d.LastJobID
	if jobID != 0 {
		if job, err := a.Client.GetJob(ctx, jobID, false); err != nil || (awx.Done(job.Status) && job.Status != "successful") {
			jobID = 0
		}
	}
	if jobID == 0 {
		id := t.AWX.TemplateID
		if id == 0 {
			var err error
			if id, err = a.Client.LookupJobTemplate(ctx, t.AWX.JobTemplate, false); err != nil {
				return Result{Err: "The template can't be found: " + err.Error()}
			}
		}
		var event map[string]any
		_ = json.Unmarshal(d.Event, &event)
		job, err := a.Client.Launch(ctx, id, false, awx.LaunchRequest{ExtraVars: map[string]any{"rvav_event": event}})
		if err != nil {
			return Result{Err: "AWX refused the launch: " + err.Error()}
		}
		jobID = job.ID
	}
	poll := a.Poll
	if poll == 0 {
		poll = 2 * time.Second
	}
	for {
		job, err := a.Client.GetJob(ctx, jobID, false)
		switch {
		case err == nil && job.Status == "successful":
			res := Result{OK: true, JobID: jobID, Response: "Job " + fmt.Sprint(jobID) + " succeeded."}
			if d.Blocking {
				res.Outcome = "continue"
			}
			return res
		case err == nil && awx.Done(job.Status):
			msg := fmt.Sprintf("AWX job %d ended %s.", jobID, job.Status)
			if d.Blocking {
				// The check ran and said no: the request stops.
				return Result{OK: true, JobID: jobID, Outcome: "stop", Reason: msg, Response: msg}
			}
			return Result{JobID: jobID, Err: msg}
		}
		select {
		case <-ctx.Done():
			return Result{JobID: jobID, Err: fmt.Sprintf("AWX job %d is still running after %s.", jobID, timeout)}
		case <-time.After(poll):
		}
	}
}
