package api

// Automatic snapshots: a VPS can be snapshotted every day or every week at a
// set hour (the PC's local time). Only automatic snapshots are deleted to
// keep the newest N; snapshots the user took are never removed.

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/abrardw/lps/internal/store"
)

var (
	scheduleInterval = time.Minute
	// scheduleLoc is the time zone schedules use; tests override it.
	scheduleLoc = time.Local
	// scheduleNow is the clock; tests move it forward.
	scheduleNow = time.Now
)

const maxKeep = 50

// lastSlot is the most recent time at or before now when the schedule is due.
func lastSlot(sc *store.SnapshotSchedule, now time.Time) time.Time {
	now = now.In(scheduleLoc)
	slot := time.Date(now.Year(), now.Month(), now.Day(), sc.Hour, 0, 0, 0, scheduleLoc)
	if sc.Frequency == store.ScheduleWeekly {
		slot = slot.AddDate(0, 0, -((int(now.Weekday()) - sc.Weekday + 7) % 7))
		if slot.After(now) {
			slot = slot.AddDate(0, 0, -7)
		}
		return slot
	}
	if slot.After(now) {
		slot = slot.AddDate(0, 0, -1)
	}
	return slot
}

// nextSlot is the first time after now when the schedule is due.
func nextSlot(sc *store.SnapshotSchedule, now time.Time) time.Time {
	last := lastSlot(sc, now)
	if sc.Frequency == store.ScheduleWeekly {
		return last.AddDate(0, 0, 7)
	}
	return last.AddDate(0, 0, 1)
}

// scheduleDue returns the slot to take now, if the schedule hasn't handled
// it yet (a PC that was off at that time catches up once).
func scheduleDue(sc *store.SnapshotSchedule, now time.Time) (time.Time, bool) {
	slot := lastSlot(sc, now)
	since := sc.CreatedAt
	if sc.LastRun != nil && sc.LastRun.After(since) {
		since = *sc.LastRun
	}
	return slot, slot.After(since)
}

// reserveSnapshot marks a VPS as being snapshotted; false if it already is.
func (s *Server) reserveSnapshot(vpsID int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.snapping[vpsID] {
		return false
	}
	if t := s.vpsTasks[vpsID]; t != nil && t.running {
		return false
	}
	s.snapping[vpsID] = true
	return true
}

func (s *Server) runSchedules(ctx context.Context) {
	list, err := s.store.Schedules(ctx)
	if err != nil {
		return
	}
	for _, sc := range list {
		if ctx.Err() != nil {
			return
		}
		slot, due := scheduleDue(sc, scheduleNow())
		if !due {
			continue
		}
		v, err := s.store.GetVPS(ctx, sc.VPSID)
		if err != nil || v.ContainerID == "" || !s.reserveSnapshot(v.ID) {
			continue // being created, restored or snapshotted: try again in a minute
		}
		// Record the slot first: a failure is reported, not retried until the next slot.
		if err := s.store.SetScheduleRun(ctx, v.ID, slot); err != nil {
			s.mu.Lock()
			delete(s.snapping, v.ID)
			s.mu.Unlock()
			continue
		}
		s.takeScheduled(ctx, v, sc, slot)
	}
}

func scheduleLabel(sc *store.SnapshotSchedule, slot time.Time) string {
	kind := "Daily"
	if sc.Frequency == store.ScheduleWeekly {
		kind = "Weekly"
	}
	return kind + " snapshot " + slot.In(scheduleLoc).Format("2006-01-02 15:04")
}

func (s *Server) takeScheduled(ctx context.Context, v *store.VPS, sc *store.SnapshotSchedule, slot time.Time) {
	label := scheduleLabel(sc, slot)
	sn, err := s.store.CreateSnapshotAuto(ctx, v.ID, label, true)
	if err != nil {
		s.mu.Lock()
		delete(s.snapping, v.ID)
		s.mu.Unlock()
		return
	}
	if err := s.takeSnapshot(ctx, v, sn, io.Discard); err != nil {
		s.fail(nil, "vps.snapshot_failed", subjVPS, v.ID, v.Name, "Automatic snapshot of %s failed: %s", v.Name, firstLine(err.Error()))
	} else {
		s.info(nil, "vps.snapshot_ready", subjVPS, v.ID, v.Name, "Automatic snapshot %s of %s is ready (%s)", quoted(label), v.Name, humanBytes(sn.SystemBytes+sn.HomeBytes))
	}
	s.pruneAuto(ctx, v, sc.Keep)
}

// pruneAuto deletes the oldest automatic snapshots beyond keep.
func (s *Server) pruneAuto(ctx context.Context, v *store.VPS, keep int) {
	list, err := s.store.ListSnapshots(ctx, v.ID) // newest first
	if err != nil {
		return
	}
	n := 0
	for _, sn := range list {
		if !sn.Auto || sn.Status == store.SnapshotCreating {
			continue
		}
		n++
		if n <= keep {
			continue
		}
		if err := s.deleteSnapshotData(ctx, sn); err != nil {
			s.log.Warn("delete old automatic snapshot", "vps", v.Name, "err", err)
			continue
		}
		s.removeExport(v, sn)
		if err := s.store.DeleteSnapshot(ctx, sn.ID); err == nil {
			s.info(nil, "vps.snapshot_deleted", subjVPS, v.ID, v.Name, "Deleted old automatic snapshot %s of %s (keeping %d)", quoted(sn.Label), v.Name, keep)
		}
	}
}

func (s *Server) scheduleLoop() {
	defer s.wg.Done()
	t := time.NewTicker(scheduleInterval)
	defer t.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-t.C:
		}
		s.runSchedules(s.ctx)
	}
}

// ---- API ----

type scheduleView struct {
	Schedule *store.SnapshotSchedule `json:"schedule"`
	Next     *time.Time              `json:"next,omitempty"`
	TimeZone string                  `json:"timeZone"`
}

func (s *Server) buildScheduleView(ctx context.Context, vpsID int64) (scheduleView, error) {
	v := scheduleView{TimeZone: scheduleNow().In(scheduleLoc).Format("MST")}
	sc, err := s.store.Schedule(ctx, vpsID)
	if err != nil || sc == nil {
		return v, err
	}
	v.Schedule = sc
	next := nextSlot(sc, scheduleNow())
	if slot, due := scheduleDue(sc, scheduleNow()); due {
		next = slot // overdue: within a minute
	}
	v.Next = &next
	return v, nil
}

func (s *Server) handleGetSchedule(w http.ResponseWriter, r *http.Request) {
	v, ok := s.loadVPS(w, r)
	if !ok {
		return
	}
	out, err := s.buildScheduleView(r.Context(), v.ID)
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, 200, out)
}

func (s *Server) handleSetSchedule(w http.ResponseWriter, r *http.Request) {
	v, ok := s.loadVPS(w, r)
	if !ok {
		return
	}
	var req struct {
		Frequency string `json:"frequency"` // off | daily | weekly
		Hour      int    `json:"hour"`
		Weekday   int    `json:"weekday"`
		Keep      int    `json:"keep"`
	}
	if !decode(w, r, &req) {
		return
	}
	ctx := r.Context()
	switch req.Frequency {
	case "off":
		if err := s.store.DeleteSchedule(ctx, v.ID); err != nil {
			s.internalError(w, err)
			return
		}
		s.info(r, "vps.schedule_off", subjVPS, v.ID, v.Name, "Automatic snapshots of %s turned off", v.Name)
	case store.ScheduleDaily, store.ScheduleWeekly:
		if req.Hour < 0 || req.Hour > 23 || req.Weekday < 0 || req.Weekday > 6 {
			writeError(w, http.StatusBadRequest, "hour must be 0-23 and weekday 0-6")
			return
		}
		if req.Keep < 1 || req.Keep > maxKeep {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("keep must be 1-%d", maxKeep))
			return
		}
		sc := &store.SnapshotSchedule{VPSID: v.ID, Frequency: req.Frequency, Hour: req.Hour, Weekday: req.Weekday, Keep: req.Keep}
		if err := s.store.SetSchedule(ctx, sc); err != nil {
			s.internalError(w, err)
			return
		}
		when := fmt.Sprintf("every day at %02d:00", req.Hour)
		if req.Frequency == store.ScheduleWeekly {
			when = fmt.Sprintf("every %s at %02d:00", time.Weekday(req.Weekday), req.Hour)
		}
		s.info(r, "vps.schedule_on", subjVPS, v.ID, v.Name, "Automatic snapshots of %s %s, keeping %d", v.Name, when, req.Keep)
		// Fewer to keep now applies right away.
		s.pruneAuto(ctx, v, req.Keep)
	default:
		writeError(w, http.StatusBadRequest, "frequency must be off, daily or weekly")
		return
	}
	out, err := s.buildScheduleView(ctx, v.ID)
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, 200, out)
}
