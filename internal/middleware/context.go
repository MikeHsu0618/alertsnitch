package middleware

import (
	"context"
	"net/http"
)

// ContextKey 用於 context 中的 key
type ContextKey string

const (
	// RequestQueryKey 是存儲 query parameters 的 key
	RequestQueryKey ContextKey = "request_query_params"
)

// WithQueryParameters 將 query parameters 注入到 context 中
func WithQueryParameters(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 獲取所有 query parameters
		queryParams := make(map[string]string)
		for key, values := range r.URL.Query() {
			if len(values) > 0 {
				queryParams[key] = values[0] // 取第一個值
			}
		}

		// 將 query parameters 存入 context
		ctx := context.WithValue(r.Context(), RequestQueryKey, queryParams)

		// 使用新的 context 繼續請求
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// GetQueryParameters 從 context 中安全地取出 query parameters
func GetQueryParameters(ctx context.Context) map[string]string {
	if params, ok := ctx.Value(RequestQueryKey).(map[string]string); ok {
		return params
	}
	return make(map[string]string)
}
