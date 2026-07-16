package notify

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/HarshShah0203/homedex/internal/export"
	"github.com/HarshShah0203/homedex/internal/store"
	"github.com/containrrr/shoutrrr"
)

type Sender interface {
	Send(context.Context, string, string) error
}

type ShoutrrrSender struct{}

func (ShoutrrrSender) Send(ctx context.Context, target, message string) error {
	result := make(chan error, 1)
	go func() { result <- shoutrrr.Send(target, message) }()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-result:
		return err
	}
}

type RuleInput struct {
	Name          string         `json:"name"`
	Kind          string         `json:"kind"`
	ThresholdDays *int           `json:"threshold_days"`
	Filters       map[string]any `json:"filters"`
	Channels      []string       `json:"channels"`
	Enabled       *bool          `json:"enabled"`
}

type Rule struct {
	ID            int64          `json:"id"`
	Name          string         `json:"name"`
	Kind          string         `json:"kind"`
	ThresholdDays *int           `json:"threshold_days"`
	Filters       map[string]any `json:"filters"`
	Channels      []string       `json:"channels"`
	ChannelCount  int            `json:"channel_count"`
	Enabled       bool           `json:"enabled"`
	CreatedAt     string         `json:"created_at"`
	UpdatedAt     string         `json:"updated_at"`
	channelURLs   []string
}

type Manager struct {
	store  *store.Store
	sender Sender
	now    func() time.Time
}

func NewManager(s *store.Store, sender Sender) *Manager {
	if sender == nil {
		sender = ShoutrrrSender{}
	}
	return &Manager{store: s, sender: sender, now: time.Now}
}

func (m *Manager) List(ctx context.Context) ([]Rule, error) {
	rows, err := m.store.DB().QueryContext(ctx, `SELECT id,name,kind,threshold_days,filters,channels,enabled,created_at,updated_at FROM notification_rules ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	rules := []Rule{}
	for rows.Next() {
		rule, err := scanRule(rows)
		if err != nil {
			return nil, err
		}
		rules = append(rules, rule)
	}
	return rules, rows.Err()
}

func (m *Manager) Create(ctx context.Context, input RuleInput) (Rule, error) {
	if err := validateInput(input); err != nil {
		return Rule{}, err
	}
	enabled := true
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	filters, _ := json.Marshal(defaultFilters(input.Filters))
	channels, _ := json.Marshal(input.Channels)
	now := m.now().UTC().Format(time.RFC3339Nano)
	result, err := m.store.DB().ExecContext(ctx, `INSERT INTO notification_rules(name,kind,threshold_days,filters,channels,enabled,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`, input.Name, input.Kind, input.ThresholdDays, string(filters), string(channels), enabled, now, now)
	if err != nil {
		return Rule{}, err
	}
	id, _ := result.LastInsertId()
	return m.get(ctx, id)
}

func (m *Manager) Update(ctx context.Context, id int64, input RuleInput) (Rule, error) {
	old, err := m.get(ctx, id)
	if err != nil {
		return Rule{}, err
	}
	if input.Name == "" {
		input.Name = old.Name
	}
	if input.Kind == "" {
		input.Kind = old.Kind
	}
	if input.ThresholdDays == nil {
		input.ThresholdDays = old.ThresholdDays
	}
	if input.Filters == nil {
		input.Filters = old.Filters
	}
	if input.Channels == nil {
		input.Channels = old.channelURLs
	}
	if input.Enabled == nil {
		enabled := old.Enabled
		input.Enabled = &enabled
	}
	if err = validateInput(input); err != nil {
		return Rule{}, err
	}
	filters, _ := json.Marshal(input.Filters)
	channels, _ := json.Marshal(input.Channels)
	result, err := m.store.DB().ExecContext(ctx, `UPDATE notification_rules SET name=?,kind=?,threshold_days=?,filters=?,channels=?,enabled=?,updated_at=? WHERE id=?`, input.Name, input.Kind, input.ThresholdDays, string(filters), string(channels), *input.Enabled, m.now().UTC().Format(time.RFC3339Nano), id)
	if err != nil {
		return Rule{}, err
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return Rule{}, sql.ErrNoRows
	}
	return m.get(ctx, id)
}

func (m *Manager) Delete(ctx context.Context, id int64) error {
	result, err := m.store.DB().ExecContext(ctx, `DELETE FROM notification_rules WHERE id=?`, id)
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (m *Manager) Test(ctx context.Context, id int64) error {
	rule, err := m.get(ctx, id)
	if err != nil {
		return err
	}
	return m.send(ctx, rule.channelURLs, "Homedex test notification: rule \""+rule.Name+"\" is configured correctly.")
}

// Evaluate implements engine.RuleEvaluator. Notification failures are returned
// to the engine for progress reporting but never roll back an inventory scan.
func (m *Manager) Evaluate(ctx context.Context, scanRunID int64) error {
	rules, err := m.enabledRules(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for _, rule := range rules {
		switch rule.Kind {
		case "expiry":
			if err = m.evaluateExpiry(ctx, rule); err != nil {
				errs = append(errs, fmt.Errorf("rule %d: %w", rule.ID, err))
			}
		case "change":
			if err = m.evaluateChanges(ctx, rule, scanRunID); err != nil {
				errs = append(errs, fmt.Errorf("rule %d: %w", rule.ID, err))
			}
		}
	}
	return errors.Join(errs...)
}

func (m *Manager) evaluateExpiry(ctx context.Context, rule Rule) error {
	if rule.ThresholdDays == nil {
		return nil
	}
	data, err := export.NewLoader(m.store.DB()).Load(ctx, export.Options{IncludePrivate: false})
	if err != nil {
		return err
	}
	for _, item := range data.Expiry {
		days, err := export.DaysRemaining(m.now(), item.ExpiresAt)
		if err != nil || days == nil || *days > *rule.ThresholdDays {
			continue
		}
		message := fmt.Sprintf("Homedex expiry: %s (%s) expires in %d day(s) at %s.", item.Name, item.Kind, *days, item.ExpiresAt)
		baseKey := fmt.Sprintf("expiry:%s:%d:%s:%d", item.EntityType, item.ID, item.ExpiresAt, *rule.ThresholdDays)
		if err = m.deliverOnce(ctx, rule, baseKey, message); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) evaluateChanges(ctx context.Context, rule Rule, scanRunID int64) error {
	rows, err := m.store.DB().QueryContext(ctx, `SELECT id,entity_type,change_kind,summary FROM changes WHERE scan_run_id=? ORDER BY id`, scanRunID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var entityType, kind, summary string
		if err = rows.Scan(&id, &entityType, &kind, &summary); err != nil {
			return err
		}
		if !filterAllows(rule.Filters, "entity_types", entityType) || !filterAllows(rule.Filters, "change_kinds", kind) {
			continue
		}
		if err = m.deliverOnce(ctx, rule, fmt.Sprintf("change:%d", id), "Homedex change: "+summary); err != nil {
			return err
		}
	}
	return rows.Err()
}

func (m *Manager) deliverOnce(ctx context.Context, rule Rule, baseKey, message string) error {
	for index, target := range rule.channelURLs {
		key := fmt.Sprintf("%s:channel:%d", baseKey, index)
		result, err := m.store.DB().ExecContext(ctx, `INSERT INTO notification_deliveries(rule_id,dedupe_key,delivered_at,error) VALUES(?,?,?,'pending') ON CONFLICT(rule_id,dedupe_key) DO UPDATE SET delivered_at=excluded.delivered_at,error='pending' WHERE notification_deliveries.error NOT IN ('','pending')`, rule.ID, key, m.now().UTC().Format(time.RFC3339Nano))
		if err != nil {
			return err
		}
		if count, _ := result.RowsAffected(); count == 0 {
			continue
		}
		err = m.sender.Send(ctx, target, message)
		errorText := ""
		if err != nil {
			errorText = err.Error()
		}
		_, _ = m.store.DB().ExecContext(context.WithoutCancel(ctx), `UPDATE notification_deliveries SET error=? WHERE rule_id=? AND dedupe_key=?`, errorText, rule.ID, key)
		if err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) send(ctx context.Context, channels []string, message string) error {
	var errs []error
	for _, target := range channels {
		if err := m.sender.Send(ctx, target, message); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (m *Manager) get(ctx context.Context, id int64) (Rule, error) {
	return scanRule(m.store.DB().QueryRowContext(ctx, `SELECT id,name,kind,threshold_days,filters,channels,enabled,created_at,updated_at FROM notification_rules WHERE id=?`, id))
}

func (m *Manager) enabledRules(ctx context.Context) ([]Rule, error) {
	rules, err := m.List(ctx)
	if err != nil {
		return nil, err
	}
	out := rules[:0]
	for _, rule := range rules {
		if rule.Enabled {
			out = append(out, rule)
		}
	}
	return out, nil
}

type scanner interface{ Scan(...any) error }

func scanRule(row scanner) (Rule, error) {
	var rule Rule
	var threshold sql.NullInt64
	var filters, channels string
	if err := row.Scan(&rule.ID, &rule.Name, &rule.Kind, &threshold, &filters, &channels, &rule.Enabled, &rule.CreatedAt, &rule.UpdatedAt); err != nil {
		return rule, err
	}
	if threshold.Valid {
		value := int(threshold.Int64)
		rule.ThresholdDays = &value
	}
	_ = json.Unmarshal([]byte(filters), &rule.Filters)
	if rule.Filters == nil {
		rule.Filters = map[string]any{}
	}
	_ = json.Unmarshal([]byte(channels), &rule.channelURLs)
	rule.ChannelCount = len(rule.channelURLs)
	for _, target := range rule.channelURLs {
		rule.Channels = append(rule.Channels, channelLabel(target))
	}
	return rule, nil
}

func validateInput(input RuleInput) error {
	if input.Kind != "expiry" && input.Kind != "change" {
		return errors.New("notification kind must be expiry or change")
	}
	if strings.TrimSpace(input.Name) == "" {
		return errors.New("notification rule name is required")
	}
	if input.Kind == "expiry" && (input.ThresholdDays == nil || *input.ThresholdDays < 0 || *input.ThresholdDays > 3650) {
		return errors.New("expiry rules require threshold_days between 0 and 3650")
	}
	if len(input.Channels) == 0 {
		return errors.New("at least one notification channel is required")
	}
	for _, target := range input.Channels {
		parsed, err := url.Parse(target)
		if err != nil || parsed.Scheme == "" {
			return errors.New("notification channels must be valid shoutrrr URLs")
		}
	}
	return nil
}

func defaultFilters(filters map[string]any) map[string]any {
	if filters == nil {
		return map[string]any{}
	}
	return filters
}

func channelLabel(target string) string {
	parsed, err := url.Parse(target)
	if err != nil || parsed.Scheme == "" {
		return "configured"
	}
	return strings.ToLower(parsed.Scheme)
}

func filterAllows(filters map[string]any, key, value string) bool {
	raw, ok := filters[key]
	if !ok {
		return true
	}
	values := []string{}
	switch list := raw.(type) {
	case []any:
		for _, item := range list {
			if text, ok := item.(string); ok {
				values = append(values, text)
			}
		}
	case []string:
		values = append(values, list...)
	}
	if len(values) == 0 {
		return true
	}
	sort.Strings(values)
	index := sort.SearchStrings(values, value)
	return index < len(values) && values[index] == value
}
