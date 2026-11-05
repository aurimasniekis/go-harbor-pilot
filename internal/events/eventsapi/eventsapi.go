// Package eventsapi exposes the event catalog, recent events, relay
// delivery history and replay, and notification history, replay and test
// delivery. Replays and tests are audited.
package eventsapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/api"
	"github.com/aurimasniekis/go-harbor-pilot/internal/audit"
	"github.com/aurimasniekis/go-harbor-pilot/internal/auth"
	cp "github.com/aurimasniekis/go-harbor-pilot/internal/controlplane"
	"github.com/aurimasniekis/go-harbor-pilot/internal/events"
	"github.com/aurimasniekis/go-harbor-pilot/internal/events/relay"
	"github.com/aurimasniekis/go-harbor-pilot/internal/notify"
	"github.com/aurimasniekis/go-harbor-pilot/internal/store"
)

// Deps are the API's dependencies.
type Deps struct {
	DB       func() *store.DB
	Relay    func() *relay.Relay
	Notify   func() *notify.Engine
	Snapshot func() *cp.Snapshot
}

// EventDTO is an outbox event.
type EventDTO struct {
	Seq        int64           `json:"seq"`
	ID         string          `json:"id"`
	Type       string          `json:"type"`
	Subject    string          `json:"subject"`
	Severity   string          `json:"severity"`
	OccurredAt time.Time       `json:"occurred_at"`
	Envelope   json.RawMessage `json:"envelope"`
}

// Register mounts the endpoints.
func Register(a *api.API, d Deps) {
	tags := []string{"Events"}
	type catOut struct {
		Body struct {
			Items []events.KindInfo `json:"items"`
		}
	}
	api.Register(a, api.Op{ID: "event-catalog", Method: http.MethodGet, Path: "/events/catalog", Summary: "Event kinds and their fields",
		Tags: tags, Permission: auth.PermConfigRead}, func(ctx context.Context, _ *struct{}) (*catOut, error) {
		out := &catOut{}
		out.Body.Items = events.Catalog
		return out, nil
	})
	type listIn struct {
		api.PageParams
		Type     string `query:"type" maxLength:"200"`
		Severity string `query:"severity" enum:"info,warning,critical," required:"false"`
	}
	type listOut struct{ Body api.Page[EventDTO] }
	api.Register(a, api.Op{ID: "list-events", Method: http.MethodGet, Path: "/events", Summary: "Recent domain events (newest first)",
		Tags: tags, Permission: auth.PermConfigRead}, func(ctx context.Context, in *listIn) (*listOut, error) {
		cur, err := api.DecodeCursor(in.Cursor)
		if err != nil {
			return nil, err
		}
		lim := in.NormalizeLimit()
		q := `SELECT seq, id, type, subject, severity, occurred_at, payload FROM event_outbox WHERE 1=1`
		var args []any
		if cur != "" {
			q += ` AND id < ?`
			args = append(args, cur)
		}
		if in.Type != "" {
			q += ` AND type = ?`
			args = append(args, in.Type)
		}
		if in.Severity != "" {
			q += ` AND severity = ?`
			args = append(args, in.Severity)
		}
		q += ` ORDER BY id DESC LIMIT ?`
		args = append(args, lim+1)
		items, err := store.Query(ctx, d.DB().R, func(r *sql.Rows) (EventDTO, error) {
			var e EventDTO
			var at int64
			var p string
			err := r.Scan(&e.Seq, &e.ID, &e.Type, &e.Subject, &e.Severity, &at, &p)
			e.OccurredAt, e.Envelope = time.UnixMilli(at).UTC(), json.RawMessage(p)
			return e, err
		}, q, args...)
		if err != nil {
			return nil, err
		}
		return &listOut{Body: api.Paginate(items, lim, func(e EventDTO) string { return e.ID })}, nil
	})

	type delIn struct {
		ID     string `path:"id"`
		Status string `query:"status" enum:"pending,deferred,delivered,dead," required:"false"`
		Limit  int    `query:"limit" minimum:"0" maximum:"500"`
	}
	type delOut struct {
		Body struct {
			Items []relay.Delivery `json:"items"`
		}
	}
	api.Register(a, api.Op{ID: "list-deliveries", Method: http.MethodGet, Path: "/event-destinations/{id}/deliveries", Summary: "Delivery history",
		Tags: tags, Permission: auth.PermConfigRead}, func(ctx context.Context, in *delIn) (*delOut, error) {
		items, err := d.Relay().Deliveries(ctx, in.ID, in.Status, in.Limit)
		if err != nil {
			return nil, err
		}
		out := &delOut{}
		out.Body.Items = items
		return out, nil
	})
	type replayIn struct {
		ID   string `path:"id"`
		Body struct {
			DeliveryID string     `json:"delivery_id,omitempty" doc:"Replay one dead delivery; empty = all dead deliveries."`
			Since      *time.Time `json:"since,omitempty" doc:"Instead, re-send every matching event since this time."`
		}
	}
	type replayOut struct {
		Body struct {
			Queued int64 `json:"queued"`
		}
	}
	api.Register(a, api.Op{ID: "replay-deliveries", Method: http.MethodPost, Path: "/event-destinations/{id}/replay", Summary: "Replay deliveries",
		Description: "Delivery is at-least-once; receivers deduplicate by event id.", Tags: tags, Permission: auth.PermEventsManage},
		func(ctx context.Context, in *replayIn) (*replayOut, error) {
			if d.Snapshot().Destinations[in.ID] == nil {
				return nil, api.NotFound("event destination")
			}
			var n int64
			var err error
			if in.Body.Since != nil {
				n, err = d.Relay().ReplaySince(ctx, in.ID, *in.Body.Since)
			} else {
				n, err = d.Relay().Replay(ctx, in.ID, in.Body.DeliveryID)
			}
			if err != nil {
				return nil, err
			}
			_ = audit.Record(ctx, d.DB().W, audit.Entry{Action: "events.replay", TargetKind: "event_destination", TargetID: in.ID,
				Details: map[string]any{"queued": n, "delivery_id": in.Body.DeliveryID, "since": in.Body.Since}})
			out := &replayOut{}
			out.Body.Queued = n
			return out, nil
		})

	ntags := []string{"Notifications"}
	type nIn struct {
		ChannelID string `query:"channel_id" maxLength:"64"`
		Status    string `query:"status" enum:"pending,digest,sent,digested,dead," required:"false"`
		Limit     int    `query:"limit" minimum:"0" maximum:"500"`
	}
	type nOut struct {
		Body struct {
			Items []notify.Notification `json:"items"`
		}
	}
	api.Register(a, api.Op{ID: "list-notifications", Method: http.MethodGet, Path: "/notifications", Summary: "Notification history",
		Tags: ntags, Permission: auth.PermConfigRead}, func(ctx context.Context, in *nIn) (*nOut, error) {
		items, err := d.Notify().List(ctx, in.ChannelID, in.Status, in.Limit)
		if err != nil {
			return nil, err
		}
		out := &nOut{}
		out.Body.Items = items
		return out, nil
	})
	type idIn struct {
		ID string `path:"id"`
	}
	type okOut struct {
		Body struct {
			Queued bool `json:"queued"`
		}
	}
	api.Register(a, api.Op{ID: "replay-notification", Method: http.MethodPost, Path: "/notifications/{id}/replay", Summary: "Replay a dead notification",
		Tags: ntags, Permission: auth.PermNotifyManage}, func(ctx context.Context, in *idIn) (*okOut, error) {
		ok, err := d.Notify().Replay(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, api.Errorf(http.StatusConflict, "conflict", "only dead notifications can be replayed")
		}
		_ = audit.Record(ctx, d.DB().W, audit.Entry{Action: "notification.replay", TargetKind: "notification", TargetID: in.ID})
		out := &okOut{}
		out.Body.Queued = true
		return out, nil
	})
	type testOut struct {
		Body struct {
			Delivered bool   `json:"delivered"`
			Status    int    `json:"status,omitempty"`
			Error     string `json:"error,omitempty"`
		}
	}
	api.Register(a, api.Op{ID: "test-notification-channel", Method: http.MethodPost, Path: "/notification-channels/{id}/test", Summary: "Send a test notification",
		Tags: ntags, Permission: auth.PermNotifyManage}, func(ctx context.Context, in *idIn) (*testOut, error) {
		res, err := d.Notify().Test(ctx, in.ID)
		if err != nil {
			return nil, api.NotFound("notification channel")
		}
		_ = audit.Record(ctx, d.DB().W, audit.Entry{Action: "notification.test", TargetKind: "notification_channel", TargetID: in.ID,
			Details: map[string]any{"delivered": res.OK, "status": res.Status}})
		out := &testOut{}
		out.Body.Delivered, out.Body.Status, out.Body.Error = res.OK, res.Status, res.Err
		return out, nil
	})
}
