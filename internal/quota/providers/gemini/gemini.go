// Package gemini implements the gemini_gcp adapter: per-project Gemini API
// quota limits from the Cloud Quotas API joined with quota usage from Cloud
// Monitoring (serviceruntime quota metrics). Authentication uses a Google
// service-account key and the documented JWT-bearer OAuth grant.
package gemini

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/domain"
	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/providers"
)

// ProviderID is the adapter identifier.
const ProviderID domain.ProviderID = "gemini_gcp"

// Documented endpoints and defaults.
const (
	DefaultService       = "generativelanguage.googleapis.com"
	DefaultQuotasBase    = "https://cloudquotas.googleapis.com"
	DefaultMonitoringURL = "https://monitoring.googleapis.com"
	Scope                = "https://www.googleapis.com/auth/cloud-platform"
	jwtBearerGrant       = "urn:ietf:params:oauth:grant-type:jwt-bearer"
	maxQuotaPages        = 10
	maxSeriesPages       = 5
	defaultMaxBuckets    = 20
	netUsageMetric       = "serviceruntime.googleapis.com/quota/rate/net_usage"
	allocationMetric     = "serviceruntime.googleapis.com/quota/allocation/usage"
)

// pacific is where Gemini per-day quotas reset (midnight Pacific time, per
// the Gemini API rate-limit documentation).
var pacific = func() *time.Location {
	l, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		return time.FixedZone("PT", -8*3600)
	}
	return l
}()

// Options configure an account.
type Options struct {
	ProjectID string `json:"project_id"`
	Service   string `json:"service"`
	// MaxBuckets bounds the number of quota buckets emitted (default 20).
	MaxBuckets int `json:"max_buckets"`
	// QuotasBaseURL and MonitoringBaseURL override the API origins (tests).
	QuotasBaseURL     string `json:"quotas_base_url"`
	MonitoringBaseURL string `json:"monitoring_base_url"`
}

// Adapter implements providers.Adapter.
type Adapter struct {
	deps providers.Deps
	mu   sync.Mutex
	// tokens caches access tokens by a hash of the key identity.
	tokens map[string]cachedToken
}

type cachedToken struct {
	value  string
	expiry time.Time
}

// New creates the adapter.
func New(deps providers.Deps) *Adapter {
	return &Adapter{deps: deps, tokens: map[string]cachedToken{}}
}

// Info implements providers.Adapter.
func (a *Adapter) Info() domain.ProviderInfo {
	s, p := providers.Sup, providers.Part
	return domain.ProviderInfo{
		ID: ProviderID, DisplayName: "Google Gemini API (Cloud project)", Family: "google", AdapterVersion: "1",
		Tier: domain.TierOfficial, DefaultPollInterval: 15 * time.Minute,
		CredentialTypes: []string{"service account JSON key (roles: Cloud Quotas Viewer, Monitoring Viewer)"},
		Capabilities: providers.Caps(map[domain.Capability]domain.CapabilityInfo{
			domain.CapQuotaLimit:      s("Cloud Quotas API quotaInfos (per project, per dimension such as model)"),
			domain.CapQuotaUsed:       p("Cloud Monitoring quota/rate/net_usage; per-dimension usage only when the series carries a matching label"),
			domain.CapQuotaRemaining:  p("derived as limit − used when usage is known"),
			domain.CapFixedReset:      s("per-day quotas reset at midnight Pacific time (documented)"),
			domain.CapRollingWindow:   p("per-minute quotas are rolling windows; forecasts are limited"),
			domain.CapModelQuota:      s("quota dimensions with a model key"),
			domain.CapOrgProjectScope: s("one Google Cloud project per account"),
			domain.CapMonetarySpend:   providers.Unsup("billing export is not read"),
			domain.CapTokenCounters:   p("token-per-minute quotas when the project has them"),
		}),
		Limitations: []string{
			"Monitoring quota metrics lag by a few minutes; per-day usage is summed since midnight Pacific.",
			"Quota metric names are discovered from quotaInfos at runtime, never hard-coded.",
			"Usage that cannot be attributed to a specific quota dimension is left unknown, not zero.",
			"Monitoring only records points when usage occurs: a project-wide quota with no point in the interval is reported as 0 used.",
		},
		DocsURLs: []string{
			"https://cloud.google.com/docs/quotas/reference/rest/v1/projects.locations.services.quotaInfos/list",
			"https://cloud.google.com/monitoring/alerts/using-quota-metrics",
			"https://ai.google.dev/gemini-api/docs/rate-limits",
			"https://developers.google.com/identity/protocols/oauth2/service-account",
		},
	}
}

func parse(cfg providers.AccountConfig) (Options, error) {
	var o Options
	if err := providers.DecodeOptions(cfg.Options, &o); err != nil {
		return o, err
	}
	if o.ProjectID == "" {
		return o, providers.NewError(domain.ErrConfiguration, "options.project_id is required")
	}
	if strings.ContainsAny(o.ProjectID, "/?#") {
		return o, providers.NewError(domain.ErrConfiguration, "options.project_id is invalid")
	}
	if o.Service == "" {
		o.Service = DefaultService
	}
	if strings.ContainsAny(o.Service, "/?#\" ") {
		return o, providers.NewError(domain.ErrConfiguration, "options.service is invalid")
	}
	if o.MaxBuckets <= 0 {
		o.MaxBuckets = defaultMaxBuckets
	}
	if o.MaxBuckets > 100 {
		o.MaxBuckets = 100
	}
	var err error
	if o.QuotasBaseURL == "" {
		o.QuotasBaseURL = DefaultQuotasBase
	} else if o.QuotasBaseURL, err = providers.ValidateBaseURL(o.QuotasBaseURL); err != nil {
		return o, err
	}
	if o.MonitoringBaseURL == "" {
		o.MonitoringBaseURL = DefaultMonitoringURL
	} else if o.MonitoringBaseURL, err = providers.ValidateBaseURL(o.MonitoringBaseURL); err != nil {
		return o, err
	}
	return o, nil
}

// serviceAccount is the subset of a Google service-account key file used.
type serviceAccount struct {
	Type         string `json:"type"`
	ClientEmail  string `json:"client_email"`
	PrivateKey   string `json:"private_key"`
	PrivateKeyID string `json:"private_key_id"`
	TokenURI     string `json:"token_uri"`
}

func keyJSON(cfg providers.AccountConfig) (string, error) {
	for _, name := range []string{"service_account", "api_key"} {
		if s, ok := cfg.Credentials.Get(name); ok {
			return s.Reveal(), nil
		}
	}
	return "", providers.NewError(domain.ErrConfiguration, `credential "service_account" (service account JSON key) is required`)
}

func parseServiceAccount(raw string) (serviceAccount, *rsa.PrivateKey, error) {
	var sa serviceAccount
	if err := json.Unmarshal([]byte(raw), &sa); err != nil {
		return sa, nil, providers.NewError(domain.ErrConfiguration, "service account credential is not valid JSON")
	}
	if sa.Type != "" && sa.Type != "service_account" {
		return sa, nil, providers.NewError(domain.ErrConfiguration, "credential is not a service account key")
	}
	if sa.ClientEmail == "" || sa.PrivateKey == "" {
		return sa, nil, providers.NewError(domain.ErrConfiguration, "service account key lacks client_email or private_key")
	}
	if sa.TokenURI == "" {
		sa.TokenURI = "https://oauth2.googleapis.com/token"
	}
	tu, err := providers.ValidateBaseURL(sa.TokenURI)
	if err != nil {
		return sa, nil, providers.NewError(domain.ErrConfiguration, "service account token_uri must be an https URL")
	}
	sa.TokenURI = tu
	block, _ := pem.Decode([]byte(sa.PrivateKey))
	if block == nil {
		return sa, nil, providers.NewError(domain.ErrConfiguration, "service account private_key is not PEM encoded")
	}
	var key *rsa.PrivateKey
	if k, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		rk, ok := k.(*rsa.PrivateKey)
		if !ok {
			return sa, nil, providers.NewError(domain.ErrConfiguration, "service account private_key is not an RSA key")
		}
		key = rk
	} else if k, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		key = k
	} else {
		return sa, nil, providers.NewError(domain.ErrConfiguration, "service account private_key cannot be parsed")
	}
	return sa, key, nil
}

// Validate implements providers.Adapter.
func (a *Adapter) Validate(cfg providers.AccountConfig) error {
	if _, err := parse(cfg); err != nil {
		return err
	}
	raw, err := keyJSON(cfg)
	if err != nil {
		return err
	}
	_, _, err = parseServiceAccount(raw)
	return err
}

// signJWT builds the RS256-signed JWT-bearer assertion.
func signJWT(sa serviceAccount, key *rsa.PrivateKey, now time.Time) (string, error) {
	hdr := map[string]string{"alg": "RS256", "typ": "JWT"}
	if sa.PrivateKeyID != "" {
		hdr["kid"] = sa.PrivateKeyID
	}
	claims := map[string]any{
		"iss": sa.ClientEmail, "scope": Scope, "aud": sa.TokenURI,
		"iat": now.Unix(), "exp": now.Add(time.Hour).Unix(),
	}
	hb, _ := json.Marshal(hdr)
	cb, _ := json.Marshal(claims)
	signing := base64.RawURLEncoding.EncodeToString(hb) + "." + base64.RawURLEncoding.EncodeToString(cb)
	sum := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(nil, key, crypto.SHA256, sum[:])
	if err != nil {
		return "", err
	}
	return signing + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

func (a *Adapter) accessToken(ctx context.Context, raw string, now time.Time) (string, error) {
	sa, key, err := parseServiceAccount(raw)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256([]byte(sa.ClientEmail + "\x00" + sa.PrivateKeyID + "\x00" + sa.TokenURI + "\x00" + sa.PrivateKey))
	cacheKey := hex.EncodeToString(h[:])
	a.mu.Lock()
	if t, ok := a.tokens[cacheKey]; ok && now.Before(t.expiry.Add(-time.Minute)) {
		a.mu.Unlock()
		return t.value, nil
	}
	a.mu.Unlock()
	assertion, err := signJWT(sa, key, now)
	if err != nil {
		return "", providers.NewError(domain.ErrConfiguration, "cannot sign service account assertion")
	}
	var tr struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int64  `json:"expires_in"`
	}
	_, err = a.deps.HTTP.DoJSON(ctx, providers.Request{Method: http.MethodPost, URL: sa.TokenURI,
		Form: url.Values{"grant_type": {jwtBearerGrant}, "assertion": {assertion}}}, &tr)
	if err != nil {
		var pe *providers.Error
		if errors.As(err, &pe) && (pe.HTTPStatus == http.StatusBadRequest || pe.HTTPStatus == http.StatusUnauthorized) {
			return "", &providers.Error{Kind: domain.ErrAuthentication, Message: "Google rejected the service account key", HTTPStatus: pe.HTTPStatus}
		}
		return "", err
	}
	if tr.AccessToken == "" {
		return "", providers.NewError(domain.ErrInvalidResponse, "token endpoint returned no access token")
	}
	exp := now.Add(time.Duration(max(tr.ExpiresIn, 60)) * time.Second)
	a.mu.Lock()
	a.tokens[cacheKey] = cachedToken{value: tr.AccessToken, expiry: exp}
	a.mu.Unlock()
	return tr.AccessToken, nil
}

// quotaInfo is the documented QuotaInfo resource (subset).
type quotaInfo struct {
	QuotaID           string `json:"quotaId"`
	Metric            string `json:"metric"`
	IsPrecise         bool   `json:"isPrecise"`
	RefreshInterval   string `json:"refreshInterval"`
	MetricDisplayName string `json:"metricDisplayName"`
	QuotaDisplayName  string `json:"quotaDisplayName"`
	MetricUnit        string `json:"metricUnit"`
	IsConcurrent      bool   `json:"isConcurrent"`
	DimensionsInfos   []struct {
		Dimensions map[string]string `json:"dimensions"`
		Details    struct {
			Value json.RawMessage `json:"value"`
		} `json:"details"`
	} `json:"dimensionsInfos"`
}

func (a *Adapter) quotaInfos(ctx context.Context, o Options, h http.Header) ([]quotaInfo, bool, error) {
	var out []quotaInfo
	token := ""
	for page := 0; page < maxQuotaPages; page++ {
		q := url.Values{"pageSize": {"100"}}
		if token != "" {
			q.Set("pageToken", token)
		}
		u := fmt.Sprintf("%s/v1/projects/%s/locations/global/services/%s/quotaInfos?%s",
			o.QuotasBaseURL, url.PathEscape(o.ProjectID), url.PathEscape(o.Service), q.Encode())
		var body struct {
			QuotaInfos    []quotaInfo `json:"quotaInfos"`
			NextPageToken string      `json:"nextPageToken"`
		}
		if _, err := a.deps.HTTP.DoJSON(ctx, providers.Request{URL: u, Header: h}, &body); err != nil {
			return nil, false, err
		}
		out = append(out, body.QuotaInfos...)
		if body.NextPageToken == "" {
			return out, false, nil
		}
		token = body.NextPageToken
	}
	return out, true, nil
}

// series is one aggregated usage value from Cloud Monitoring.
type series struct {
	labels map[string]string
	value  float64
	end    time.Time
}

func (a *Adapter) timeSeries(ctx context.Context, o Options, h http.Header, metric string, start, end time.Time, align time.Duration) ([]series, error) {
	filter := fmt.Sprintf(`metric.type=%q AND resource.type="consumer_quota" AND resource.label.service=%q`, metric, o.Service)
	q := url.Values{
		"filter":             {filter},
		"interval.startTime": {start.UTC().Format(time.RFC3339)},
		"interval.endTime":   {end.UTC().Format(time.RFC3339)},
	}
	if align > 0 {
		q.Set("aggregation.alignmentPeriod", strconv.FormatInt(int64(align/time.Second), 10)+"s")
		q.Set("aggregation.perSeriesAligner", "ALIGN_SUM")
	}
	var out []series
	for page := 0; page < maxSeriesPages; page++ {
		u := fmt.Sprintf("%s/v3/projects/%s/timeSeries?%s", o.MonitoringBaseURL, url.PathEscape(o.ProjectID), q.Encode())
		var body struct {
			TimeSeries []struct {
				Metric struct {
					Labels map[string]string `json:"labels"`
				} `json:"metric"`
				Points []struct {
					Interval struct {
						EndTime time.Time `json:"endTime"`
					} `json:"interval"`
					Value struct {
						Int64Value  *string  `json:"int64Value"`
						DoubleValue *float64 `json:"doubleValue"`
					} `json:"value"`
				} `json:"points"`
			} `json:"timeSeries"`
			NextPageToken string `json:"nextPageToken"`
		}
		if _, err := a.deps.HTTP.DoJSON(ctx, providers.Request{URL: u, Header: h}, &body); err != nil {
			return nil, err
		}
		for _, ts := range body.TimeSeries {
			// Points are returned newest first; take the newest.
			var best *series
			for _, p := range ts.Points {
				var v float64
				switch {
				case p.Value.Int64Value != nil:
					n, err := strconv.ParseInt(*p.Value.Int64Value, 10, 64)
					if err != nil {
						continue
					}
					v = float64(n)
				case p.Value.DoubleValue != nil:
					v = *p.Value.DoubleValue
				default:
					continue
				}
				if best == nil || p.Interval.EndTime.After(best.end) {
					best = &series{labels: ts.Metric.Labels, value: v, end: p.Interval.EndTime}
				}
			}
			if best != nil {
				out = append(out, *best)
			}
		}
		if body.NextPageToken == "" {
			break
		}
		q.Set("pageToken", body.NextPageToken)
	}
	return out, nil
}

// refreshDuration parses refreshInterval strings such as "minute", "day" or
// "10 seconds". Zero means not a rate quota.
func refreshDuration(s string) time.Duration {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return 0
	}
	n := 1
	if f := strings.Fields(s); len(f) == 2 {
		v, err := strconv.Atoi(f[0])
		if err != nil || v <= 0 {
			return 0
		}
		n, s = v, f[1]
	}
	unit := map[string]time.Duration{
		"second": time.Second, "seconds": time.Second, "minute": time.Minute, "minutes": time.Minute,
		"hour": time.Hour, "hours": time.Hour, "day": 24 * time.Hour, "days": 24 * time.Hour,
	}[s]
	return time.Duration(n) * unit
}

func parseLimit(raw json.RawMessage) (float64, bool) {
	s := strings.Trim(strings.TrimSpace(string(raw)), `"`)
	if s == "" || s == "null" {
		return 0, false
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil || v <= 0 { // -1 is unlimited; 0 means the quota is not granted
		return 0, false
	}
	return float64(v), true
}

func nextPacificMidnight(t time.Time) time.Time {
	lt := t.In(pacific)
	return time.Date(lt.Year(), lt.Month(), lt.Day()+1, 0, 0, 0, 0, pacific).UTC()
}

func pacificDayStart(t time.Time) time.Time {
	lt := t.In(pacific)
	return time.Date(lt.Year(), lt.Month(), lt.Day(), 0, 0, 0, 0, pacific).UTC()
}

type candidate struct {
	obs   domain.BucketObservation
	daily bool
	util  float64 // -1 when unknown
}

func dimsKey(d map[string]string) string {
	keys := make([]string, 0, len(d))
	for k := range d {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+d[k])
	}
	return strings.Join(parts, ",")
}

// usageFor finds usage for a quota dimension entry. Series are matched on
// quota_metric; dimension-specific entries need every dimension to appear as
// a series label, otherwise usage is unknown (never attributed blindly).
func usageFor(ss []series, metric string, dims map[string]string) (float64, time.Time, bool) {
	var total float64
	var end time.Time
	found := false
	for _, s := range ss {
		if s.labels["quota_metric"] != metric {
			continue
		}
		match := true
		for k, v := range dims {
			if lv, ok := s.labels[k]; !ok || lv != v {
				match = false
				break
			}
		}
		if !match {
			continue
		}
		total += s.value
		if s.end.After(end) {
			end = s.end
		}
		found = true
	}
	return total, end, found
}

// Fetch implements providers.Adapter.
func (a *Adapter) Fetch(ctx context.Context, req providers.FetchRequest) (*providers.FetchResult, error) {
	o, err := parse(req.Config)
	if err != nil {
		return nil, err
	}
	raw, err := keyJSON(req.Config)
	if err != nil {
		return nil, err
	}
	now := req.Now.UTC()
	tok, err := a.accessToken(ctx, raw, now)
	if err != nil {
		return nil, err
	}
	h := providers.BearerHeader(tok)
	infos, truncated, err := a.quotaInfos(ctx, o, h)
	if err != nil {
		return nil, err
	}
	res := &providers.FetchResult{SourceTime: &now}
	id := "project: " + o.ProjectID
	res.MaskedIdentifier = &id
	if truncated {
		res.Notes = append(res.Notes, "quota list truncated after 10 pages")
	}

	// Usage series per refresh interval (net_usage, summed) and allocation.
	intervals := map[time.Duration]bool{}
	hasAllocation := false
	for _, qi := range infos {
		if d := refreshDuration(qi.RefreshInterval); d > 0 {
			intervals[d] = true
		} else {
			hasAllocation = true
		}
	}
	usage := map[time.Duration][]series{}
	var usageErr error
	for d := range intervals {
		start := now.Add(-d)
		align := d
		if d == 24*time.Hour {
			// Per-day quotas count since midnight Pacific.
			start = pacificDayStart(now)
			align = now.Sub(start).Truncate(time.Second)
		}
		if align < time.Minute {
			align = time.Minute
			start = now.Add(-time.Minute)
		}
		ss, err := a.timeSeries(ctx, o, h, netUsageMetric, start, now, align)
		if err != nil {
			usageErr = err
			continue
		}
		usage[d] = ss
	}
	if hasAllocation {
		ss, err := a.timeSeries(ctx, o, h, allocationMetric, now.Add(-48*time.Hour), now, 0)
		if err != nil {
			usageErr = err
		} else {
			usage[0] = ss
		}
	}
	if usageErr != nil {
		pe := providers.AsError(usageErr)
		res.Notes = append(res.Notes, "quota usage unavailable from Cloud Monitoring ("+string(pe.Kind)+"); limits shown without usage")
	}

	var cands []candidate
	for _, qi := range infos {
		d := refreshDuration(qi.RefreshInterval)
		for _, di := range qi.DimensionsInfos {
			limit, ok := parseLimit(di.Details.Value)
			if !ok {
				continue
			}
			label := qi.QuotaDisplayName
			if label == "" {
				label = qi.MetricDisplayName
			}
			if label == "" {
				label = qi.QuotaID
			}
			spec := domain.BucketSpec{Key: "quota:" + qi.QuotaID, Label: label, Scope: domain.ScopeProject, Official: true,
				Unit: domain.UnitRequests}
			if dk := dimsKey(di.Dimensions); dk != "" {
				spec.Key += ":" + dk
			}
			if m := di.Dimensions["model"]; m != "" {
				spec.Scope, spec.Model = domain.ScopeModel, m
				spec.Label += " · " + m
			}
			// Units are not typed in quotaInfos; token quotas are named so.
			if strings.Contains(strings.ToLower(qi.Metric+" "+qi.QuotaID), "token") {
				spec.Unit = domain.UnitTokens
			}
			ob := domain.BucketObservation{Limit: providers.Float(limit), Quality: domain.QualityProviderReported,
				Source: "gcp:cloudquotas+monitoring", SourceTime: &now}
			switch {
			case d == 24*time.Hour:
				spec.Kind, spec.Window, spec.ResetSemantics, spec.Period = domain.KindFixedWindow, d, domain.ResetFixedPeriodic, 24*time.Hour
				r := nextPacificMidnight(now)
				ob.ResetAt = &r
			case d > 0:
				spec.Kind, spec.Window, spec.ResetSemantics = domain.KindRollingWindow, d, domain.ResetRolling
			default:
				spec.Kind, spec.ResetSemantics = domain.KindRateLimit, domain.ResetNone
			}
			ob.Spec = spec
			c := candidate{daily: d == 24*time.Hour, util: -1}
			if ss, ok := usage[d]; ok {
				if used, end, found := usageFor(ss, qi.Metric, di.Dimensions); found {
					rem := math.Max(limit-used, 0)
					ob.Used, ob.Remaining = providers.Float(used), providers.Float(rem)
					if !end.IsZero() {
						e := end.UTC()
						ob.SourceTime = &e
					}
					c.util = used / limit
				} else if len(di.Dimensions) == 0 {
					// The series list for this interval was retrieved and has
					// no point for this quota metric: no usage was recorded.
					ob.Used, ob.Remaining = providers.Float(0), providers.Float(limit)
					c.util = 0
				}
			}
			c.obs = ob
			cands = append(cands, c)
		}
	}
	sort.SliceStable(cands, func(i, j int) bool {
		if cands[i].daily != cands[j].daily {
			return cands[i].daily
		}
		return cands[i].util > cands[j].util
	})
	if len(cands) > o.MaxBuckets {
		res.Notes = append(res.Notes, fmt.Sprintf("%d quotas found; showing %d (per-day quotas first, then highest utilization)", len(cands), o.MaxBuckets))
		cands = cands[:o.MaxBuckets]
	}
	for _, c := range cands {
		res.Buckets = append(res.Buckets, c.obs)
	}
	if len(res.Buckets) == 0 {
		res.Notes = append(res.Notes, "no finite quota limits reported for "+o.Service)
	}
	return res, nil
}
