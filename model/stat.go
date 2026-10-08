package model

import (
	"github.com/jinzhu/gorm"
	"go-file/common"
	"sync"
	"time"
	"unicode/utf8"
)

// Visit statistics are aggregated in memory and periodically written to the
// database, so a request never waits on a database write and the feature works
// without Redis.

const (
	statHourLayout = "2006-01-02 15"
	statDateLayout = "2006-01-02"
	// StatMaxURLLength keeps the URL inside the indexed column size.
	StatMaxURLLength = 255
	// statMaxPendingKeys bounds memory use when many distinct IPs/URLs arrive between two flushes.
	statMaxPendingKeys = 50000
	// statFlushBatchSize rows are written per transaction; each one finishes in milliseconds.
	statFlushBatchSize = 500
	statFlushInterval  = 30 * time.Second
	statCleanInterval  = time.Hour
)

// StatHour stores the request count of one hour, e.g. "2026-10-08 14".
type StatHour struct {
	Hour string `gorm:"column:hour;primary_key;size:13"`
	Hits int64  `gorm:"column:hits"`
}

func (StatHour) TableName() string { return "stat_hours" }

// StatIP stores the request count of one IP on one day.
type StatIP struct {
	Date string `gorm:"column:date;primary_key;size:10"`
	IP   string `gorm:"column:ip;primary_key;size:64"`
	Hits int64  `gorm:"column:hits"`
}

func (StatIP) TableName() string { return "stat_ips" }

// StatURL stores the request count of one URL on one day.
type StatURL struct {
	Date string `gorm:"column:date;primary_key;size:10"`
	URL  string `gorm:"column:url;primary_key;size:255"`
	Hits int64  `gorm:"column:hits"`
}

func (StatURL) TableName() string { return "stat_urls" }

type statKey struct {
	date  string
	value string
}

type statBuffer struct {
	hours map[string]int64
	ips   map[statKey]int64
	urls  map[statKey]int64
}

func newStatBuffer() *statBuffer {
	return &statBuffer{
		hours: make(map[string]int64),
		ips:   make(map[statKey]int64),
		urls:  make(map[statKey]int64),
	}
}

func (b *statBuffer) empty() bool {
	return len(b.hours) == 0 && len(b.ips) == 0 && len(b.urls) == 0
}

var (
	statMu      sync.Mutex
	statPending = newStatBuffer()
	// statFlushMu serializes flushes so the update-then-insert upsert never races with itself.
	statFlushMu   sync.Mutex
	statStartOnce sync.Once
)

// RecordVisit counts one request. An empty url only counts toward the totals and the IP.
func RecordVisit(ip string, url string, t time.Time) {
	t = t.In(time.Local)
	date := t.Format(statDateLayout)
	hour := t.Format(statHourLayout)
	url = truncateUTF8(url, StatMaxURLLength)

	statMu.Lock()
	defer statMu.Unlock()
	statPending.hours[hour]++
	if ip != "" {
		key := statKey{date, ip}
		if _, ok := statPending.ips[key]; ok || len(statPending.ips) < statMaxPendingKeys {
			statPending.ips[key]++
		}
	}
	if url != "" {
		key := statKey{date, url}
		if _, ok := statPending.urls[key]; ok || len(statPending.urls) < statMaxPendingKeys {
			statPending.urls[key]++
		}
	}
}

func truncateUTF8(s string, max int) string {
	if len(s) <= max {
		return s
	}
	s = s[:max]
	for len(s) > 0 && !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}

// FlushStats writes buffered counters to the database in small transactions,
// so a large burst never holds the SQLite write lock for long.
func FlushStats() error {
	statFlushMu.Lock()
	defer statFlushMu.Unlock()

	statMu.Lock()
	buffer := statPending
	statPending = newStatBuffer()
	statMu.Unlock()

	if buffer.empty() || DB == nil {
		return nil
	}
	rows := buffer.rows()
	for start := 0; start < len(rows); start += statFlushBatchSize {
		end := start + statFlushBatchSize
		if end > len(rows) {
			end = len(rows)
		}
		if err := writeStatRows(rows[start:end]); err != nil {
			// Committed batches are kept; the rest is retried on the next flush.
			restoreStatRows(rows[start:])
			return err
		}
	}
	return nil
}

type statTable int

const (
	statTableHour statTable = iota
	statTableIP
	statTableURL
)

// statRow is one counter to add; for hours, value holds the hour and date is unused.
type statRow struct {
	table statTable
	date  string
	value string
	hits  int64
}

func (b *statBuffer) rows() []statRow {
	rows := make([]statRow, 0, len(b.hours)+len(b.ips)+len(b.urls))
	for hour, hits := range b.hours {
		rows = append(rows, statRow{table: statTableHour, value: hour, hits: hits})
	}
	for key, hits := range b.ips {
		rows = append(rows, statRow{table: statTableIP, date: key.date, value: key.value, hits: hits})
	}
	for key, hits := range b.urls {
		rows = append(rows, statRow{table: statTableURL, date: key.date, value: key.value, hits: hits})
	}
	return rows
}

func restoreStatRows(rows []statRow) {
	statMu.Lock()
	defer statMu.Unlock()
	for _, row := range rows {
		switch row.table {
		case statTableHour:
			statPending.hours[row.value] += row.hits
		case statTableIP:
			statPending.ips[statKey{row.date, row.value}] += row.hits
		case statTableURL:
			statPending.urls[statKey{row.date, row.value}] += row.hits
		}
	}
}

func writeStatRows(rows []statRow) error {
	tx := DB.Begin()
	if tx.Error != nil {
		return tx.Error
	}
	for _, row := range rows {
		var err error
		switch row.table {
		case statTableHour:
			err = upsertHits(tx.Exec, "UPDATE stat_hours SET hits = hits + ? WHERE hour = ?",
				"INSERT INTO stat_hours (hour, hits) VALUES (?, ?)", row.hits, row.value)
		case statTableIP:
			err = upsertHits(tx.Exec, "UPDATE stat_ips SET hits = hits + ? WHERE date = ? AND ip = ?",
				"INSERT INTO stat_ips (date, ip, hits) VALUES (?, ?, ?)", row.hits, row.date, row.value)
		case statTableURL:
			err = upsertHits(tx.Exec, "UPDATE stat_urls SET hits = hits + ? WHERE date = ? AND url = ?",
				"INSERT INTO stat_urls (date, url, hits) VALUES (?, ?, ?)", row.hits, row.date, row.value)
		}
		if err != nil {
			tx.Rollback()
			return err
		}
	}
	return tx.Commit().Error
}

type execFunc func(sql string, values ...interface{}) *gorm.DB

// upsertHits uses UPDATE then INSERT, which behaves the same on SQLite and MySQL.
func upsertHits(exec execFunc, updateSQL string, insertSQL string, hits int64, keys ...interface{}) error {
	updateArgs := append([]interface{}{hits}, keys...)
	result := exec(updateSQL, updateArgs...)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected > 0 {
		return nil
	}
	insertArgs := append(append([]interface{}{}, keys...), hits)
	return exec(insertSQL, insertArgs...).Error
}

// CleanExpiredStats removes data older than the retention window.
func CleanExpiredStats() error {
	if DB == nil {
		return nil
	}
	firstDate := StatFirstDate(common.StatRetentionDays, time.Now())
	if err := DB.Exec("DELETE FROM stat_hours WHERE hour < ?", firstDate+" 00").Error; err != nil {
		return err
	}
	if err := DB.Exec("DELETE FROM stat_ips WHERE date < ?", firstDate).Error; err != nil {
		return err
	}
	return DB.Exec("DELETE FROM stat_urls WHERE date < ?", firstDate).Error
}

// ClearStats removes all collected visit data, including unflushed counters.
func ClearStats() error {
	statFlushMu.Lock()
	defer statFlushMu.Unlock()
	statMu.Lock()
	statPending = newStatBuffer()
	statMu.Unlock()
	for _, table := range []string{"stat_hours", "stat_ips", "stat_urls"} {
		if err := DB.Exec("DELETE FROM " + table).Error; err != nil {
			return err
		}
	}
	return nil
}

// StartStatCollector periodically flushes counters and removes expired data.
func StartStatCollector() {
	statStartOnce.Do(func() {
		go func() {
			if err := CleanExpiredStats(); err != nil {
				common.SysError("failed to clean expired stats: " + err.Error())
			}
			flushTicker := time.NewTicker(statFlushInterval)
			cleanTicker := time.NewTicker(statCleanInterval)
			defer flushTicker.Stop()
			defer cleanTicker.Stop()
			for {
				select {
				case <-flushTicker.C:
					if err := FlushStats(); err != nil {
						common.SysError("failed to flush stats: " + err.Error())
					}
				case <-cleanTicker.C:
					if err := CleanExpiredStats(); err != nil {
						common.SysError("failed to clean expired stats: " + err.Error())
					}
				}
			}
		}()
	})
}

// StatFirstDate returns the first day of a range of `days` days ending today.
func StatFirstDate(days int, now time.Time) string {
	if days < 1 {
		days = 1
	}
	return now.In(time.Local).AddDate(0, 0, -(days - 1)).Format(statDateLayout)
}

type StatSummary struct {
	TodayPV int64 `json:"todayPV"`
	TodayUV int64 `json:"todayUV"`
	PV      int64 `json:"pv"`
	UV      int64 `json:"uv"`
	Days    int   `json:"days"`
}

func GetStatSummary(days int, now time.Time) (summary StatSummary, err error) {
	today := now.In(time.Local).Format(statDateLayout)
	firstDate := StatFirstDate(days, now)
	summary.Days = days

	var row struct{ Total int64 }
	if err = DB.Table("stat_hours").Select("COALESCE(SUM(hits), 0) AS total").
		Where("hour >= ?", today+" 00").Scan(&row).Error; err != nil {
		return
	}
	summary.TodayPV = row.Total
	if err = DB.Table("stat_hours").Select("COALESCE(SUM(hits), 0) AS total").
		Where("hour >= ?", firstDate+" 00").Scan(&row).Error; err != nil {
		return
	}
	summary.PV = row.Total
	if err = DB.Table("stat_ips").Select("COUNT(*) AS total").
		Where("date = ?", today).Scan(&row).Error; err != nil {
		return
	}
	summary.TodayUV = row.Total
	if err = DB.Table("stat_ips").Select("COUNT(DISTINCT ip) AS total").
		Where("date >= ?", firstDate).Scan(&row).Error; err != nil {
		return
	}
	summary.UV = row.Total
	return
}

type StatTrendItem struct {
	Name string `json:"name"`
	PV   int64  `json:"pv"`
	UV   *int64 `json:"uv,omitempty"`
}

type StatTrend struct {
	Granularity string          `json:"granularity"`
	Items       []StatTrendItem `json:"items"`
}

// GetStatTrend returns hourly data for today when days is 1, otherwise daily data.
// Periods without visits are filled with zero so the chart has a continuous axis.
func GetStatTrend(days int, now time.Time) (trend StatTrend, err error) {
	now = now.In(time.Local)
	firstDate := StatFirstDate(days, now)

	var hours []StatHour
	if err = DB.Where("hour >= ?", firstDate+" 00").Find(&hours).Error; err != nil {
		return
	}

	if days == 1 {
		trend.Granularity = "hour"
		pv := make(map[string]int64, len(hours))
		for _, h := range hours {
			pv[h.Hour] += h.Hits
		}
		start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
		trend.Items = []StatTrendItem{}
		for t := start; !t.After(now); t = t.Add(time.Hour) {
			key := t.Format(statHourLayout)
			trend.Items = append(trend.Items, StatTrendItem{Name: key, PV: pv[key]})
		}
		return
	}

	trend.Granularity = "day"
	pv := make(map[string]int64)
	for _, h := range hours {
		if len(h.Hour) >= len(statDateLayout) {
			pv[h.Hour[:len(statDateLayout)]] += h.Hits
		}
	}
	var uvRows []struct {
		Date  string
		Total int64
	}
	if err = DB.Table("stat_ips").Select("date, COUNT(*) AS total").
		Where("date >= ?", firstDate).Group("date").Scan(&uvRows).Error; err != nil {
		return
	}
	uv := make(map[string]int64, len(uvRows))
	for _, r := range uvRows {
		uv[r.Date] = r.Total
	}
	trend.Items = make([]StatTrendItem, 0, days)
	for i := days - 1; i >= 0; i-- {
		key := now.AddDate(0, 0, -i).Format(statDateLayout)
		dayUV := uv[key]
		trend.Items = append(trend.Items, StatTrendItem{Name: key, PV: pv[key], UV: &dayUV})
	}
	return
}

type StatRankItem struct {
	Name  string `json:"name"`
	Value int64  `json:"value"`
}

func GetTopStatIPs(days int, limit int, now time.Time) ([]StatRankItem, error) {
	return getTopStat("stat_ips", "ip", days, limit, now)
}

func GetTopStatURLs(days int, limit int, now time.Time) ([]StatRankItem, error) {
	return getTopStat("stat_urls", "url", days, limit, now)
}

func getTopStat(table string, column string, days int, limit int, now time.Time) ([]StatRankItem, error) {
	items := []StatRankItem{}
	err := DB.Table(table).
		Select(column+" AS name, SUM(hits) AS value").
		Where("date >= ?", StatFirstDate(days, now)).
		Group(column).
		Order("value DESC, name ASC").
		Limit(limit).
		Scan(&items).Error
	if err != nil {
		return nil, err
	}
	return items, nil
}
