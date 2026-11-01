package httpapi

import (
	"context"
	"errors"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"cpamp-viewer/server/internal/sub2api"
)

const (
	sub2APIAccountsPageSize = 200
	sub2APIMaxAccounts      = 2000
	sub2APIUsageWorkers     = 8
)

type sub2APIAccountList struct {
	Items    []sub2APIAccount `json:"items"`
	Total    int              `json:"total"`
	Page     int              `json:"page"`
	PageSize int              `json:"page_size"`
	Pages    int              `json:"pages"`
}

type sub2APIAccount struct {
	ID             int64          `json:"id"`
	Name           string         `json:"name"`
	Email          string         `json:"email"`
	Platform       string         `json:"platform"`
	Type           string         `json:"type"`
	QuotaDimension string         `json:"quota_dimension"`
	Status         string         `json:"status"`
	ErrorMessage   string         `json:"error_message"`
	UpdatedAt      string         `json:"updated_at"`
	Extra          map[string]any `json:"extra"`
	Credentials    struct {
		Email    string `json:"email"`
		PlanType string `json:"plan_type"`
	} `json:"credentials"`
	QuotaLimit       *float64 `json:"quota_limit"`
	QuotaUsed        *float64 `json:"quota_used"`
	QuotaDailyLimit  *float64 `json:"quota_daily_limit"`
	QuotaDailyUsed   *float64 `json:"quota_daily_used"`
	QuotaWeeklyLimit *float64 `json:"quota_weekly_limit"`
	QuotaWeeklyUsed  *float64 `json:"quota_weekly_used"`
	QuotaDailyReset  string   `json:"quota_daily_reset_at"`
	QuotaWeeklyReset string   `json:"quota_weekly_reset_at"`
}

type sub2APIUsageInfo struct {
	Source             string                       `json:"source"`
	UpdatedAt          string                       `json:"updated_at"`
	FiveHour           *sub2APIUsageProgress        `json:"five_hour"`
	SevenDay           *sub2APIUsageProgress        `json:"seven_day"`
	SevenDaySonnet     *sub2APIUsageProgress        `json:"seven_day_sonnet"`
	SevenDayFable      *sub2APIUsageProgress        `json:"seven_day_fable"`
	ThirtyDay          *sub2APIUsageProgress        `json:"thirty_day"`
	GeminiSharedDaily  *sub2APIUsageProgress        `json:"gemini_shared_daily"`
	GeminiProDaily     *sub2APIUsageProgress        `json:"gemini_pro_daily"`
	GeminiFlashDaily   *sub2APIUsageProgress        `json:"gemini_flash_daily"`
	GeminiSharedMinute *sub2APIUsageProgress        `json:"gemini_shared_minute"`
	GeminiProMinute    *sub2APIUsageProgress        `json:"gemini_pro_minute"`
	GeminiFlashMinute  *sub2APIUsageProgress        `json:"gemini_flash_minute"`
	AntigravityQuota   map[string]sub2APIModelQuota `json:"antigravity_quota"`
	GrokRequestQuota   *sub2APIGrokQuota            `json:"grok_request_quota"`
	GrokTokenQuota     *sub2APIGrokQuota            `json:"grok_token_quota"`
	SubscriptionTier   string                       `json:"subscription_tier"`
	SubscriptionRaw    string                       `json:"subscription_tier_raw"`
	IsForbidden        bool                         `json:"is_forbidden"`
	NeedsVerify        bool                         `json:"needs_verify"`
	IsBanned           bool                         `json:"is_banned"`
	NeedsReauth        bool                         `json:"needs_reauth"`
	ErrorCode          string                       `json:"error_code"`
	Error              string                       `json:"error"`
}

type sub2APIUsageProgress struct {
	Utilization      *float64 `json:"utilization"`
	ResetsAt         string   `json:"resets_at"`
	RemainingSeconds *float64 `json:"remaining_seconds"`
}

type sub2APIModelQuota struct {
	Utilization *float64 `json:"utilization"`
	ResetTime   string   `json:"reset_time"`
}

type sub2APIGrokQuota struct {
	Limit     *float64 `json:"limit"`
	Remaining *float64 `json:"remaining"`
	ResetUnix *float64 `json:"reset_unix"`
	ResetAt   string   `json:"reset_at"`
}

func (s *Server) loadSub2APIQuota(ctx context.Context) ([]quotaAccount, error) {
	accounts, err := s.fetchSub2APIAccounts(ctx)
	if err != nil {
		return nil, err
	}
	usage, usageErrors, err := s.fetchSub2APIUsage(ctx, accounts)
	if err != nil {
		return nil, err
	}
	projected := make([]quotaAccount, 0, len(accounts))
	for _, account := range accounts {
		projected = append(projected, projectSub2APIAccount(account, usage[account.ID], usageErrors[account.ID]))
	}
	sort.Slice(projected, func(i, j int) bool {
		if projected[i].Provider == projected[j].Provider {
			return projected[i].DisplayName < projected[j].DisplayName
		}
		return projected[i].Provider < projected[j].Provider
	})
	return projected, nil
}

func (s *Server) fetchSub2APIAccounts(ctx context.Context) ([]sub2APIAccount, error) {
	accounts := make([]sub2APIAccount, 0)
	complete := false
	for page := 1; page <= sub2APIMaxAccounts; page++ {
		query := url.Values{
			"page":      {strconv.Itoa(page)},
			"page_size": {strconv.Itoa(sub2APIAccountsPageSize)},
		}
		var response sub2APIAccountList
		if err := s.sub2api.GetJSON(ctx, "/api/v1/admin/accounts", query, &response); err != nil {
			return nil, err
		}
		if response.Items == nil || response.Total < 0 {
			return nil, errors.New("Sub2API returned an invalid account list")
		}
		if response.Total > sub2APIMaxAccounts || len(accounts)+len(response.Items) > sub2APIMaxAccounts {
			return nil, errors.New("Sub2API account count exceeds viewer limit")
		}
		accounts = append(accounts, response.Items...)
		pageSize := response.PageSize
		if pageSize <= 0 {
			pageSize = sub2APIAccountsPageSize
		}
		if len(response.Items) == 0 || (response.Pages > 0 && page >= response.Pages) || (response.Total > 0 && len(accounts) >= response.Total) || (response.PageSize > 0 && len(response.Items) < pageSize) {
			complete = true
			break
		}
	}
	if !complete {
		return nil, errors.New("Sub2API account count exceeds viewer limit")
	}
	return accounts, nil
}

func (s *Server) fetchSub2APIUsage(ctx context.Context, accounts []sub2APIAccount) (map[int64]sub2APIUsageInfo, map[int64]bool, error) {
	usage := make(map[int64]sub2APIUsageInfo, len(accounts))
	usageErrors := make(map[int64]bool)
	jobs := make(chan sub2APIAccount)
	workCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var workers sync.WaitGroup
	var mu sync.Mutex
	var authErr error
	for worker := 0; worker < sub2APIUsageWorkers; worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for account := range jobs {
				if workCtx.Err() != nil {
					return
				}
				var item sub2APIUsageInfo
				query := url.Values{"source": {"passive"}}
				err := s.sub2api.GetJSON(workCtx, "/api/v1/admin/accounts/"+strconv.FormatInt(account.ID, 10)+"/usage", query, &item)
				mu.Lock()
				if err == nil {
					usage[account.ID] = item
				} else {
					usageErrors[account.ID] = true
					var upstream *sub2api.UpstreamError
					if errors.As(err, &upstream) && (upstream.Status == http.StatusUnauthorized || upstream.Status == http.StatusForbidden) && authErr == nil {
						authErr = err
						cancel()
					}
				}
				mu.Unlock()
			}
		}()
	}
enqueue:
	for _, account := range accounts {
		if !supportsSub2APIPassiveUsage(account) {
			continue
		}
		select {
		case jobs <- account:
		case <-workCtx.Done():
			break enqueue
		}
	}
	close(jobs)
	workers.Wait()
	if authErr != nil {
		return nil, nil, authErr
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	return usage, usageErrors, nil
}

func supportsSub2APIPassiveUsage(account sub2APIAccount) bool {
	if !strings.EqualFold(account.Platform, "anthropic") {
		return false
	}
	return account.Type == "oauth" || account.Type == "setup-token"
}

func projectSub2APIAccount(account sub2APIAccount, usage sub2APIUsageInfo, usageError bool) quotaAccount {
	provider := canonicalSub2APIProvider(account.Platform, account.Type)
	accountID := pseudonym("sub2api-account:" + strconv.FormatInt(account.ID, 10))
	displayName := sub2APIAccountDisplayName(account)
	if displayName == "" {
		displayName = provider + " " + accountID[5:11]
	}
	status := cleanText(account.Status, 40)
	if status == "" {
		status = "unknown"
	}
	statusMessage := ""
	if account.ErrorMessage != "" {
		statusMessage = "账号存在错误"
	}
	if usageError || usage.Error != "" {
		statusMessage = strings.TrimSpace(statusMessage + " 用量快照暂不可用")
	}
	if usage.NeedsReauth {
		statusMessage = strings.TrimSpace(statusMessage + " 需要重新授权")
	}
	if usage.IsForbidden || usage.NeedsVerify || usage.IsBanned {
		statusMessage = strings.TrimSpace(statusMessage + " 上游账号不可用")
	}

	updatedAt := parseSub2APITime(usage.UpdatedAt)
	accountUpdatedAt := parseSub2APITime(account.UpdatedAt)
	windows := make([]quotaWindow, 0, 8)
	fiveHour := usage.FiveHour
	// Sub2API emits an all-zero placeholder when no passive 5H sample exists.
	// Without a sample time, reset or actual usage, it does not prove full quota.
	if usage.Source == "passive" && updatedAt == 0 && fiveHour != nil &&
		fiveHour.Utilization != nil && *fiveHour.Utilization == 0 &&
		parseSub2APITime(fiveHour.ResetsAt) == 0 &&
		(fiveHour.RemainingSeconds == nil || *fiveHour.RemainingSeconds == 0) {
		fiveHour = nil
	}
	appendSub2APIProgress(&windows, "five_hour", "5 小时额度", "five_hour", "", fiveHour, updatedAt)
	appendSub2APIProgress(&windows, "seven_day", "7 天额度", "weekly", "", usage.SevenDay, updatedAt)
	appendSub2APIProgress(&windows, "seven_day_sonnet", "7 天额度", "weekly", "Sonnet", usage.SevenDaySonnet, updatedAt)
	appendSub2APIProgress(&windows, "seven_day_fable", "7 天额度", "weekly", "Fable", usage.SevenDayFable, updatedAt)
	appendSub2APIProgress(&windows, "thirty_day", "30 天额度", "monthly", "", usage.ThirtyDay, updatedAt)
	appendSub2APIProgress(&windows, "gemini_shared_daily", "日额度", "daily", "共享", usage.GeminiSharedDaily, updatedAt)
	appendSub2APIProgress(&windows, "gemini_pro_daily", "日额度", "daily", "Pro", usage.GeminiProDaily, updatedAt)
	appendSub2APIProgress(&windows, "gemini_flash_daily", "日额度", "daily", "Flash", usage.GeminiFlashDaily, updatedAt)
	appendSub2APIProgress(&windows, "gemini_shared_minute", "分钟额度", "minute", "共享", usage.GeminiSharedMinute, updatedAt)
	appendSub2APIProgress(&windows, "gemini_pro_minute", "分钟额度", "minute", "Pro", usage.GeminiProMinute, updatedAt)
	appendSub2APIProgress(&windows, "gemini_flash_minute", "分钟额度", "minute", "Flash", usage.GeminiFlashMinute, updatedAt)
	appendSub2APIModelWindows(&windows, usage.AntigravityQuota, updatedAt)
	appendSub2APIGrokWindow(&windows, "grok_request", "请求额度", usage.GrokRequestQuota, updatedAt)
	appendSub2APIGrokWindow(&windows, "grok_token", "Token 额度", usage.GrokTokenQuota, updatedAt)
	if strings.EqualFold(account.Platform, "openai") {
		appendSub2APICodexSnapshot(&windows, account.Extra, account.QuotaDimension)
	}
	appendSub2APIConfiguredQuota(&windows, "quota", "总额度", account.QuotaLimit, account.QuotaUsed, "", accountUpdatedAt)
	appendSub2APIConfiguredQuota(&windows, "quota_daily", "日额度", account.QuotaDailyLimit, account.QuotaDailyUsed, account.QuotaDailyReset, accountUpdatedAt)
	appendSub2APIConfiguredQuota(&windows, "quota_weekly", "周额度", account.QuotaWeeklyLimit, account.QuotaWeeklyUsed, account.QuotaWeeklyReset, accountUpdatedAt)

	plan := cleanText(usage.SubscriptionTier, 80)
	if plan == "" {
		plan = cleanText(usage.SubscriptionRaw, 80)
	}
	if plan == "" {
		plan = cleanText(stringValue(account.Extra["plan_type"], account.Extra["subscription_tier"], account.Credentials.PlanType), 80)
	}
	return quotaAccount{
		ID:            accountID,
		Source:        "sub2api",
		Provider:      provider,
		DisplayName:   displayName,
		Plan:          plan,
		Status:        status,
		StatusMessage: statusMessage,
		Disabled:      status != "active",
		Windows:       windows,
		UpdatedAtMS:   max(updatedAt, accountUpdatedAt, parseSub2APITime(stringValue(account.Extra["codex_usage_updated_at"]))),
	}
}

func sub2APIAccountDisplayName(account sub2APIAccount) string {
	for _, value := range []string{account.Credentials.Email, stringValue(account.Extra["email"]), account.Email} {
		email := strings.TrimSpace(value)
		if len(email) <= 160 && strings.Count(email, "@") == 1 && !strings.ContainsAny(email, " \t\r\n") {
			if safe := cleanText(email, 160); safe == email {
				return safe
			}
		}
	}
	return cleanText(account.Name, 160)
}

func appendSub2APICodexSnapshot(windows *[]quotaWindow, extra map[string]any, dimension string) {
	if len(extra) == 0 {
		return
	}
	observedAt := quotaTimestampMS(extra["codex_usage_updated_at"])
	pool := "unknown"
	switch strings.ToLower(strings.TrimSpace(dimension)) {
	case "", "global":
		// Sub2API treats an omitted dimension as global for legacy accounts.
		pool = "codex_main"
	case "spark":
		pool = "codex_spark"
	}
	appendSub2APICodexWindow(windows, extra, "5h", "5 小时额度", "five_hour", pool, observedAt)
	appendSub2APICodexWindow(windows, extra, "7d", "7 天额度", "weekly", pool, observedAt)
	appendSub2APICodexWindow(windows, extra, "primary", "已观测额度", "", pool, observedAt)
	appendSub2APICodexWindow(windows, extra, "secondary", "已观测额度", "", pool, observedAt)
}

func appendSub2APICodexWindow(windows *[]quotaWindow, extra map[string]any, slot, label, kind, pool string, observedAt int64) {
	prefix := "codex_" + slot + "_"
	minutes, validMinutes := quotaNumber(extra[prefix+"window_minutes"])
	if !validMinutes || minutes <= 0 {
		return
	}
	resetAt := quotaTimestampMS(extra[prefix+"reset_at"])
	if resetAt == 0 && observedAt > 0 {
		if seconds, valid := quotaNumber(extra[prefix+"reset_after_seconds"]); valid && seconds > 0 {
			resetAt = quotaRelativeReset(observedAt, seconds)
		}
	}
	if resetAt > 0 && resetAt <= time.Now().UnixMilli() {
		return
	}
	used, validUsed := quotaNumber(extra[prefix+"used_percent"])
	validUsed = validUsed && used >= 0
	if !validUsed && resetAt == 0 {
		return
	}
	for _, existing := range *windows {
		if existing.Pool == pool && existing.WindowMins == minutes && existing.ResetAtMS == resetAt && existing.Used == clamp(used) && existing.UnknownUsed == !validUsed {
			return
		}
	}
	*windows = append(*windows, quotaWindow{
		ID:               prefix + "snapshot",
		Pool:             pool,
		Label:            label,
		Used:             clamp(used),
		Remaining:        clamp(100 - used),
		ResetAtMS:        resetAt,
		WindowMins:       minutes,
		ObservedAt:       observedAt,
		WindowKind:       kind,
		UnknownUsed:      !validUsed,
		UnknownRemaining: !validUsed,
	})
}

func canonicalSub2APIProvider(value, accountType string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "anthropic", "claude":
		return "claude"
	case "openai":
		if accountType == "oauth" || accountType == "setup-token" {
			return "codex"
		}
		return "openai"
	case "xai", "grok":
		return "xai"
	default:
		value = cleanText(strings.ToLower(strings.TrimSpace(value)), 80)
		if value == "" {
			return "unknown"
		}
		return value
	}
}

func appendSub2APIProgress(windows *[]quotaWindow, id, label, kind, scope string, progress *sub2APIUsageProgress, observedAt int64) {
	if progress == nil {
		return
	}
	used, valid := float64(0), progress.Utilization != nil && finiteSub2APINumber(*progress.Utilization) && *progress.Utilization >= 0
	if valid {
		used = *progress.Utilization
	}
	resetAt := parseSub2APITime(progress.ResetsAt)
	if resetAt == 0 && progress.RemainingSeconds != nil && finiteSub2APINumber(*progress.RemainingSeconds) && *progress.RemainingSeconds > 0 && observedAt > 0 {
		resetAt = observedAt + int64(*progress.RemainingSeconds*1000)
	}
	windowMinutes := sub2APIWindowMinutes(kind)
	*windows = append(*windows, quotaWindow{
		ID: id, Label: label, Used: clamp(used), Remaining: clamp(100 - used),
		ResetAtMS: resetAt, WindowMins: windowMinutes, ObservedAt: observedAt,
		WindowKind: kind, ModelScope: cleanText(scope, 80),
		UnknownUsed: !valid, UnknownRemaining: !valid,
	})
}

func appendSub2APIModelWindows(windows *[]quotaWindow, quotas map[string]sub2APIModelQuota, observedAt int64) {
	keys := make([]string, 0, len(quotas))
	for key := range quotas {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		quota := quotas[key]
		name := cleanText(key, 80)
		if name == "" || name == "敏感错误详情已隐藏" || name == "内部错误详情已隐藏" {
			continue
		}
		appendSub2APIProgress(windows, "antigravity-"+pseudonym(name)[5:], "模型额度", "model", name, &sub2APIUsageProgress{Utilization: quota.Utilization, ResetsAt: quota.ResetTime}, observedAt)
	}
}

func appendSub2APIGrokWindow(windows *[]quotaWindow, id, label string, quota *sub2APIGrokQuota, observedAt int64) {
	if quota == nil {
		return
	}
	valid := quota.Limit != nil && quota.Remaining != nil && finiteSub2APINumber(*quota.Limit) && finiteSub2APINumber(*quota.Remaining) && *quota.Limit > 0 && *quota.Remaining >= 0
	used := float64(0)
	if valid {
		used = 100 - (*quota.Remaining / *quota.Limit * 100)
	}
	resetAt := parseSub2APITime(quota.ResetAt)
	if resetAt == 0 && quota.ResetUnix != nil && finiteSub2APINumber(*quota.ResetUnix) && *quota.ResetUnix > 0 {
		resetAt = int64(*quota.ResetUnix * 1000)
	}
	*windows = append(*windows, quotaWindow{
		ID: id, Label: label, Used: clamp(used), Remaining: clamp(100 - used),
		ResetAtMS: resetAt, ObservedAt: observedAt,
		UnknownUsed: !valid, UnknownRemaining: !valid,
	})
}

func appendSub2APIConfiguredQuota(windows *[]quotaWindow, id, label string, limit, used *float64, resetAt string, observedAt int64) {
	if limit == nil || !finiteSub2APINumber(*limit) || *limit <= 0 {
		return
	}
	valid := used != nil && finiteSub2APINumber(*used) && *used >= 0
	usedPercent := float64(0)
	if valid {
		usedPercent = *used / *limit * 100
	}
	*windows = append(*windows, quotaWindow{
		ID: id, Label: label, Used: clamp(usedPercent), Remaining: clamp(100 - usedPercent),
		ResetAtMS: parseSub2APITime(resetAt), ObservedAt: observedAt,
		UnknownUsed: !valid, UnknownRemaining: !valid,
	})
}

func sub2APIWindowMinutes(kind string) float64 {
	switch kind {
	case "five_hour":
		return 300
	case "weekly":
		return 10080
	case "monthly":
		return 43200
	case "daily":
		return 1440
	case "minute":
		return 1
	default:
		return 0
	}
}

func parseSub2APITime(value string) int64 {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	timestamp, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return 0
	}
	return timestamp.UnixMilli()
}

func finiteSub2APINumber(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}
