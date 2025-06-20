# AlertSnitch Loki 功能增強建議

## 專案概況

AlertSnitch 是一個將 Prometheus AlertManager 警報寫入不同後端儲存系統的工具，包含 MySQL、PostgreSQL 和 Loki。該專案是從 yakshaving.art/alertsnitch 分支而來，增加了 Loki 支援。

## 目前 Loki 功能分析

### 已實現功能 ✅

1. **基本連接和配置**
   - HTTP/HTTPS 連接支援
   - 基本認證（Basic Auth）
   - 多租戶支援（Tenant ID）
   - TLS 設定（CA 憑證、客戶端憑證）
   - 代理伺服器支援（遵循 Prometheus 標準）

2. **批次處理機制**
   - 可配置的批次大小（預設 100）
   - 批次刷新超時（預設 5 秒）
   - 重試機制（預設 3 次）
   - 非同步批次處理

3. **資料結構化**
   - 將警報按狀態分組為不同的 stream
   - 支援查詢參數作為額外標籤
   - 結構化日誌輸出（JSON 格式）

4. **監控和觀測**
   - 健康檢查端點
   - Prometheus 指標支援
   - 詳細的錯誤日誌

5. **高可用性設計**
   - 連接池管理
   - 超時配置
   - 並發安全

## 可以增強的功能 🚀

### 1. 資料查詢和檢索增強

#### 問題：
- 目前只支援寫入，缺乏查詢功能
- 沒有提供 LogQL 查詢介面

#### 建議：
```go
// 新增查詢功能介面
type QueryInterface interface {
    QueryAlerts(ctx context.Context, query string, start, end time.Time) ([]internal.Alert, error)
    QueryRange(ctx context.Context, query string, start, end time.Time, step time.Duration) (*QueryResult, error)
    GetLabels(ctx context.Context) ([]string, error)
    GetLabelValues(ctx context.Context, label string) ([]string, error)
}

// 實現基本 LogQL 查詢支援
func (c *lokiClient) QueryAlerts(ctx context.Context, query string, start, end time.Time) ([]internal.Alert, error) {
    // 實現 LogQL 查詢邏輯
}
```

### 2. 資料保留和生命週期管理

#### 問題：
- 沒有資料保留策略配置
- 缺乏資料壓縮和歸檔機制

#### 建議：
```go
type RetentionConfig struct {
    Enabled        bool
    RetentionDays  int
    CompressAfter  time.Duration
    DeleteAfter    time.Duration
}

// 實現資料清理功能
func (c *lokiClient) CleanupOldData(ctx context.Context, olderThan time.Time) error {
    // 實現資料清理邏輯
}
```

### 3. 進階批次處理優化

#### 問題：
- 批次處理缺乏智能調整機制
- 沒有根據系統負載動態調整批次大小

#### 建議：
```go
type AdaptiveBatchConfig struct {
    MinBatchSize     int
    MaxBatchSize     int
    TargetLatency    time.Duration
    ScaleUpFactor    float64
    ScaleDownFactor  float64
}

// 實現自適應批次處理
func (c *lokiClient) adaptBatchSize(latency time.Duration, successRate float64) {
    // 根據延遲和成功率動態調整批次大小
}
```

### 4. 更豐富的標籤和元數據管理

#### 問題：
- 允許的標籤過於受限（僅 12 個預定義標籤）
- 缺乏動態標籤配置

#### 建議：
```go
type LabelConfig struct {
    AllowedLabels    []string
    RequiredLabels   []string
    LabelTransforms  map[string]string
    DynamicLabels    bool
}

// 更靈活的標籤處理
func buildStreamLabelsWithConfig(data *internal.AlertGroup, config LabelConfig) map[string]string {
    // 支援動態標籤配置
}
```

### 5. 多區域和災難恢復

#### 問題：
- 缺乏多區域支援
- 沒有故障轉移機制

#### 建議：
```go
type MultiRegionConfig struct {
    PrimaryEndpoint   string
    SecondaryEndpoints []string
    FailoverThreshold  int
    HealthCheckInterval time.Duration
}

// 實現多區域支援
func (c *lokiClient) writeToMultipleRegions(ctx context.Context, payload payload) error {
    // 多區域寫入邏輯
}
```

### 6. 資料壓縮和格式優化

#### 問題：
- 沒有資料壓縮
- JSON 格式可能不是最優的

#### 建議：
```go
type CompressionConfig struct {
    Enabled    bool
    Algorithm  string // gzip, snappy, lz4
    Level      int
}

// 實現資料壓縮
func compressPayload(data []byte, config CompressionConfig) ([]byte, error) {
    // 壓縮邏輯
}
```

### 7. 進階監控和告警

#### 問題：
- 缺乏詳細的效能指標
- 沒有自定義告警規則

#### 建議：
```go
// 新增更多 Prometheus 指標
var (
    LokiBatchFlushDuration = prometheus.NewHistogramVec(
        prometheus.HistogramOpts{
            Name: "alertsnitch_loki_batch_flush_duration_seconds",
            Help: "Time taken to flush batches to Loki",
        },
        []string{"status"},
    )
    
    LokiStreamCardinality = prometheus.NewGaugeVec(
        prometheus.GaugeOpts{
            Name: "alertsnitch_loki_stream_cardinality",
            Help: "Number of unique streams",
        },
        []string{"receiver"},
    )
)
```

### 8. 配置熱重載

#### 問題：
- 配置變更需要重啟服務
- 缺乏配置驗證

#### 建議：
```go
type ConfigWatcher struct {
    configPath string
    onChange   func(config LokiConfig) error
}

// 實現配置熱重載
func (c *lokiClient) ReloadConfig(newConfig LokiConfig) error {
    // 配置熱重載邏輯
}
```

### 9. 資料一致性和完整性檢查

#### 問題：
- 缺乏資料完整性驗證
- 沒有重複資料檢測

#### 建議：
```go
type IntegrityChecker struct {
    checksumEnabled bool
    deduplication   bool
}

// 實現資料完整性檢查
func (c *lokiClient) verifyDataIntegrity(data *internal.AlertGroup) error {
    // 資料完整性檢查邏輯
}
```

### 10. 進階查詢語言支援

#### 問題：
- 沒有 AlertSnitch 特定的查詢語言
- 缺乏預建查詢模板

#### 建議：
```go
type QueryTemplate struct {
    Name        string
    Description string
    LogQLQuery  string
    Parameters  map[string]string
}

// 預建查詢模板
var CommonQueries = map[string]QueryTemplate{
    "alerts_by_severity": {
        Name:       "Alerts by Severity",
        LogQLQuery: `{service_name="alertsnitch"} | json | severity="{{.severity}}"`,
    },
    "recent_firing_alerts": {
        Name:       "Recent Firing Alerts",
        LogQLQuery: `{service_name="alertsnitch",alert_status="firing"} | json`,
    },
}
```

## 實施優先級建議

### 高優先級 🔴
1. **資料查詢功能** - 提供基本的 LogQL 查詢支援
2. **進階監控指標** - 增加更詳細的效能指標
3. **動態標籤配置** - 提供更靈活的標籤管理

### 中優先級 🟡
1. **自適應批次處理** - 根據系統負載動態調整
2. **資料壓縮** - 減少儲存和傳輸成本
3. **配置熱重載** - 提升運維效率

### 低優先級 🟢
1. **多區域支援** - 企業級高可用性需求
2. **資料保留管理** - 長期運營需求
3. **進階查詢模板** - 使用者體驗改善

## 技術債務和重構建議

### 程式碼結構優化
1. 將 `loki.go` 拆分為多個檔案（client、batch、query、config）
2. 增加介面抽象，提升測試性
3. 改善錯誤處理和日誌記錄

### 測試覆蓋率提升
1. 增加整合測試
2. 效能測試自動化
3. 混沌工程測試

### 文件完善
1. API 文件生成
2. 最佳實踐指南
3. 故障排除手冊

## 總結

目前的 Loki 整合已經相當完善，具備了生產環境的基本需求。主要的改進空間在於查詢功能、監控觀測、和運維便利性方面。建議按照優先級逐步實施這些增強功能，以提升整體系統的可用性和可維護性。