# AlertSnitch + HolmesGPT AI RCA 開發路線圖

> 打造告警到根因分析的完整 AI 體驗

## 架構概覽

```
AlertManager → AlertSnitch → Loki → Grafana Plugin → HolmesGPT → AI RCA
```

---

## 🎯 Phase 1: 核心功能 (MVP)

### 1.1 單告警 RCA 分析
- [ ] **API 端點**: `POST /api/investigate`
- [ ] Grafana Plugin 顯示告警列表
- [ ] 點擊告警觸發 HolmesGPT 調查
- [ ] 顯示結構化 RCA 報告
  - Alert Explanation
  - Key Findings
  - Conclusions and Possible Root Causes
  - Next Steps
  - App or Infra?
- [ ] 顯示 AI 調用的工具記錄 (tool_calls)

### 1.2 AlertSnitch 數據優化
- [ ] 添加 `fingerprint` 作為 Loki stream label
- [ ] 確保 K8s 資源標籤正確提取 (namespace, pod, deployment, node)
- [ ] 修復時間戳碰撞問題（批次處理時）
- [ ] 修復告警丟棄問題（隊列滿時返回 503）

---

## 🚀 Phase 2: 進階功能

### 2.1 流式 RCA 體驗
- [ ] **API 端點**: `POST /api/stream/investigate`
- [ ] 使用 SSE (Server-Sent Events) 接收流式結果
- [ ] 即時顯示 AI 調查進度
  - `start_tool_calling` - 顯示「正在執行 kubectl describe...」
  - `tool_calling_result` - 顯示工具執行結果
  - `token_count` - 顯示 token 使用量
  - `ai_answer_end` - 顯示最終報告
- [ ] 進度條或步驟指示器 UI

### 2.2 互動式故障排查
- [ ] **API 端點**: `POST /api/issue_chat`
- [ ] 在 RCA 結果下方添加聊天輸入框
- [ ] 維護 `conversation_history` 狀態
- [ ] 支持追問功能：
  - 「能看更多日誌嗎？」
  - 「這個問題上週發生過嗎？」
  - 「如何修復這個問題？」
- [ ] 顯示 `follow_up_actions` 建議操作

### 2.3 工作負載健康檢查
- [ ] **API 端點**: `POST /api/workload_health_check`
- [ ] 新增「健康檢查」面板
- [ ] 列出關鍵 Deployment/StatefulSet
- [ ] 一鍵檢查任意工作負載健康狀態
- [ ] 定時自動健康檢查（可選）

---

## 🔗 Phase 3: 智能關聯分析

### 3.1 告警關聯分析
- [ ] 查詢同時間窗口（±5分鐘）的相關告警
- [ ] 將多個告警的 context 一起發送給 HolmesGPT
- [ ] AI 分析告警間的關聯性和共同根因
- [ ] 視覺化顯示告警關係圖

### 3.2 歷史模式分析
- [ ] 利用 AlertSnitch 的 Loki 歷史數據
- [ ] 查詢同一 fingerprint 的歷史告警
- [ ] 計算統計數據：
  - 過去 7 天觸發次數
  - 平均持續時間
  - 尖峰時段
- [ ] 將歷史模式作為 context 發送給 AI
- [ ] AI 分析是否為重複問題、週期性問題

### 3.3 告警趨勢儀表板
- [ ] 告警頻率趨勢圖
- [ ] 按 severity/namespace/service 分組統計
- [ ] 識別「問題熱點」
- [ ] AI 生成週報/日報摘要

---

## ⚡ Phase 4: 自動化與整合

### 4.1 Approval Flow 整合
- [ ] 處理 `approval_required` 事件
- [ ] 顯示「批准/拒絕」按鈕
- [ ] 支持審批後繼續執行
- [ ] 審計日誌記錄

### 4.2 Runbook 自動化
- [ ] 解析 HolmesGPT 返回的 `instructions`
- [ ] 關聯內部 Runbook 文檔
- [ ] 一鍵執行建議的修復步驟
- [ ] 修復操作審計追蹤

### 4.3 通知整合
- [ ] RCA 結果推送到 Slack
- [ ] 支持 @mention 相關團隊
- [ ] 告警升級通知

---

## 🛠️ 技術實作要點

### Grafana Plugin 開發
```typescript
// HolmesGPT 客戶端
interface HolmesGPTClient {
  investigate(alert: Alert): Promise<RCAResult>;
  streamInvestigate(alert: Alert): AsyncIterable<SSEEvent>;
  chat(issue: Issue, history: Message[]): Promise<ChatResult>;
  healthCheck(resource: K8sResource): Promise<HealthResult>;
}

// SSE 事件類型
type SSEEvent = 
  | { type: 'start_tool_calling', tool_name: string, id: string }
  | { type: 'tool_calling_result', tool_call_id: string, result: any }
  | { type: 'ai_message', content: string }
  | { type: 'ai_answer_end', sections: RCASections, analysis: string }
  | { type: 'approval_required', pending_approvals: Approval[] }
  | { type: 'error', error_code: number, msg: string };
```

### AlertSnitch 改進
```go
// 新增的 stream labels
allowedLabels["fingerprint"] = true
allowedLabels["deployment"] = true
allowedLabels["statefulset"] = true
allowedLabels["daemonset"] = true
allowedLabels["replicaset"] = true

// 時間戳去重
func ensureUniqueTimestamp(baseTime time.Time, index int) time.Time {
    return baseTime.Add(time.Duration(index) * time.Nanosecond)
}

// Back-pressure 機制
if queueFull {
    return http.StatusServiceUnavailable // 讓 AlertManager 重試
}
```

### HolmesGPT 請求格式
```json
{
  "source": "prometheus",
  "title": "{{ .Labels.alertname }}",
  "description": "{{ .Annotations.description }}",
  "subject": {
    "namespace": "{{ .Labels.namespace }}",
    "pod": "{{ .Labels.pod }}",
    "deployment": "{{ .Labels.deployment }}"
  },
  "context": {
    "fingerprint": "{{ .Fingerprint }}",
    "firing_since": "{{ .StartsAt }}",
    "previous_occurrences": 5,
    "related_alerts": [...]
  },
  "include_tool_calls": true,
  "model": "fast-model"
}
```

---

## 📋 優先級排序

| 優先級 | 功能 | 預估工時 | 依賴 |
|--------|------|----------|------|
| P0 | 單告警 RCA (MVP) | 2 週 | - |
| P0 | AlertSnitch 數據優化 | 1 週 | - |
| P1 | 流式 RCA 體驗 | 1 週 | P0 |
| P1 | 互動式故障排查 | 1 週 | P0 |
| P2 | 工作負載健康檢查 | 1 週 | P0 |
| P2 | 告警關聯分析 | 2 週 | P0 |
| P2 | 歷史模式分析 | 2 週 | P0 |
| P3 | Approval Flow | 1 週 | P1 |
| P3 | Runbook 自動化 | 2 週 | P1 |
| P3 | 通知整合 | 1 週 | P0 |

---

## 🔗 參考資源

- [HolmesGPT HTTP API 文檔](https://holmesgpt.dev/reference/http-api/)
- [HolmesGPT GitHub](https://github.com/robusta-dev/holmesgpt)
- [Grafana Plugin 開發指南](https://grafana.com/developers/plugin-tools/)
- [Loki LogQL 文檔](https://grafana.com/docs/loki/latest/query/)

---

## 📝 備註

- HolmesGPT 現為 **CNCF Sandbox Project**
- 支持多種 AI Provider: OpenAI, Anthropic, Azure, Ollama 等
- 支持多種數據源: Kubernetes, Prometheus, Loki, DataDog, Grafana 等

---

*最後更新: 2025-12-18*

