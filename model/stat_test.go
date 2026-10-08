package model

import (
	"fmt"
	"go-file/common"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/jinzhu/gorm"
)

func resetStatBuffer(t *testing.T) {
	t.Helper()
	statMu.Lock()
	statPending = newStatBuffer()
	statMu.Unlock()
	t.Cleanup(func() {
		statMu.Lock()
		statPending = newStatBuffer()
		statMu.Unlock()
	})
}

func TestRecordVisitAggregatesInMemory(t *testing.T) {
	resetStatBuffer(t)
	now := time.Date(2026, 10, 8, 14, 30, 0, 0, time.Local)
	RecordVisit("1.1.1.1", "/", now)
	RecordVisit("1.1.1.1", "/", now)
	RecordVisit("2.2.2.2", "", now)

	if got := statPending.hours["2026-10-08 14"]; got != 3 {
		t.Fatalf("hour hits = %d, want 3", got)
	}
	if got := statPending.ips[statKey{"2026-10-08", "1.1.1.1"}]; got != 2 {
		t.Fatalf("ip hits = %d, want 2", got)
	}
	if got := len(statPending.urls); got != 1 {
		t.Fatalf("empty URL must not be ranked, got %d URL keys", got)
	}
}

func TestRecordVisitBoundsDistinctKeys(t *testing.T) {
	resetStatBuffer(t)
	now := time.Now()
	statMu.Lock()
	for i := 0; i < statMaxPendingKeys; i++ {
		statPending.urls[statKey{"x", string(rune(i))}] = 1
	}
	statMu.Unlock()
	RecordVisit("1.1.1.1", "/new", now)
	if len(statPending.urls) != statMaxPendingKeys {
		t.Fatalf("buffer grew past its limit: %d", len(statPending.urls))
	}
	if statPending.hours[now.Format(statHourLayout)] != 1 {
		t.Fatal("total traffic must still be counted when the key limit is reached")
	}
}

func TestTruncateUTF8KeepsValidText(t *testing.T) {
	long := "/" + strings.Repeat("统", 200)
	got := truncateUTF8(long, StatMaxURLLength)
	if len(got) > StatMaxURLLength || !utf8.ValidString(got) {
		t.Fatalf("truncated URL is invalid: len=%d", len(got))
	}
}

func TestStatFirstDate(t *testing.T) {
	now := time.Date(2026, 10, 8, 1, 0, 0, 0, time.Local)
	if got := StatFirstDate(1, now); got != "2026-10-08" {
		t.Fatalf("1 day starts %s", got)
	}
	if got := StatFirstDate(7, now); got != "2026-10-02" {
		t.Fatalf("7 days start %s", got)
	}
}

// openStatTestDB uses a real SQLite file; it is skipped when the binary is built without cgo.
func openStatTestDB(t *testing.T) {
	t.Helper()
	db, err := gorm.Open("sqlite3", filepath.Join(t.TempDir(), "stat.db"))
	if err == nil {
		err = db.DB().Ping()
	}
	if err != nil {
		t.Skipf("SQLite is unavailable (requires cgo): %v", err)
	}
	if err := db.AutoMigrate(&StatHour{}, &StatIP{}, &StatURL{}).Error; err != nil {
		t.Fatal(err)
	}
	previous := DB
	DB = db
	t.Cleanup(func() {
		DB = previous
		_ = db.Close()
	})
	resetStatBuffer(t)
}

func TestStatsRoundTripThroughDatabase(t *testing.T) {
	openStatTestDB(t)
	now := time.Now().In(time.Local)
	yesterday := now.AddDate(0, 0, -1)

	RecordVisit("1.1.1.1", "/", now)
	RecordVisit("1.1.1.1", "/explorer", now)
	RecordVisit("2.2.2.2", "/", now)
	RecordVisit("3.3.3.3", "/", yesterday)
	if err := FlushStats(); err != nil {
		t.Fatal(err)
	}
	// A second flush must add to existing rows instead of failing on the primary key.
	RecordVisit("1.1.1.1", "/", now)
	if err := FlushStats(); err != nil {
		t.Fatal(err)
	}

	summary, err := GetStatSummary(7, now)
	if err != nil {
		t.Fatal(err)
	}
	if summary.TodayPV != 4 || summary.TodayUV != 2 || summary.PV != 5 || summary.UV != 3 {
		t.Fatalf("unexpected summary: %+v", summary)
	}

	urls, err := GetTopStatURLs(7, 10, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(urls) != 2 || urls[0].Name != "/" || urls[0].Value != 4 {
		t.Fatalf("unexpected URL ranking: %+v", urls)
	}
	ips, err := GetTopStatIPs(1, 1, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(ips) != 1 || ips[0].Name != "1.1.1.1" || ips[0].Value != 3 {
		t.Fatalf("unexpected IP ranking: %+v", ips)
	}

	daily, err := GetStatTrend(7, now)
	if err != nil {
		t.Fatal(err)
	}
	if daily.Granularity != "day" || len(daily.Items) != 7 {
		t.Fatalf("daily trend must have 7 points, got %+v", daily)
	}
	last := daily.Items[6]
	if last.PV != 4 || last.UV == nil || *last.UV != 2 {
		t.Fatalf("unexpected data for today: %+v", last)
	}
	hourly, err := GetStatTrend(1, now)
	if err != nil {
		t.Fatal(err)
	}
	if hourly.Granularity != "hour" || len(hourly.Items) != now.Hour()+1 {
		t.Fatalf("hourly trend must cover every hour so far, got %d points", len(hourly.Items))
	}

	if err := ClearStats(); err != nil {
		t.Fatal(err)
	}
	summary, _ = GetStatSummary(7, now)
	if summary.PV != 0 || summary.UV != 0 {
		t.Fatalf("stats remain after clearing: %+v", summary)
	}
}

func TestCleanExpiredStatsKeepsRetentionWindow(t *testing.T) {
	openStatTestDB(t)
	now := time.Now()
	RecordVisit("1.1.1.1", "/old", now.AddDate(0, 0, -common.StatRetentionDays))
	RecordVisit("1.1.1.1", "/new", now)
	if err := FlushStats(); err != nil {
		t.Fatal(err)
	}
	if err := CleanExpiredStats(); err != nil {
		t.Fatal(err)
	}
	var count int
	DB.Table("stat_urls").Count(&count)
	if count != 1 {
		t.Fatalf("expected only the recent URL to remain, got %d rows", count)
	}
}

func TestFailedBatchIsRestoredForRetry(t *testing.T) {
	resetStatBuffer(t)
	now := time.Date(2026, 10, 8, 9, 0, 0, 0, time.Local)
	RecordVisit("1.1.1.1", "/", now)
	RecordVisit("1.1.1.1", "/", now)
	RecordVisit("2.2.2.2", "/help", now)

	statMu.Lock()
	rows := statPending.rows()
	statPending = newStatBuffer()
	statMu.Unlock()
	if len(rows) != 5 {
		t.Fatalf("expected 1 hour + 2 IP + 2 URL rows, got %d", len(rows))
	}
	restoreStatRows(rows)
	if got := statPending.hours["2026-10-08 09"]; got != 3 {
		t.Fatalf("restored hour hits = %d, want 3", got)
	}
	if got := statPending.ips[statKey{"2026-10-08", "1.1.1.1"}]; got != 2 {
		t.Fatalf("restored IP hits = %d, want 2", got)
	}
	if got := statPending.urls[statKey{"2026-10-08", "/help"}]; got != 1 {
		t.Fatalf("restored URL hits = %d, want 1", got)
	}
}

func TestFlushWritesBurstsInSeveralBatches(t *testing.T) {
	openStatTestDB(t)
	now := time.Now()
	visitors := statFlushBatchSize*2 + 7
	for i := 0; i < visitors; i++ {
		RecordVisit(fmt.Sprintf("10.0.%d.%d", i/256, i%256), "/", now)
	}
	if err := FlushStats(); err != nil {
		t.Fatal(err)
	}
	var count int
	DB.Table("stat_ips").Count(&count)
	if count != visitors {
		t.Fatalf("stored %d IPs, want %d", count, visitors)
	}
	summary, err := GetStatSummary(1, now)
	if err != nil {
		t.Fatal(err)
	}
	if summary.TodayPV != int64(visitors) {
		t.Fatalf("today PV = %d, want %d", summary.TodayPV, visitors)
	}
}

func TestSQLiteUsesWALMode(t *testing.T) {
	openStatTestDB(t)
	enableSQLiteWAL(DB)
	var mode struct{ JournalMode string }
	if err := DB.Raw("PRAGMA journal_mode").Scan(&mode).Error; err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(mode.JournalMode, "wal") {
		t.Fatalf("journal mode = %q, want wal", mode.JournalMode)
	}
}
