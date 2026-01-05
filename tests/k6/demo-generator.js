import http from 'k6/http';
import { check, sleep } from 'k6';
import { Rate, Counter } from 'k6/metrics';

// ============================================================================
// Custom Metrics
// ============================================================================
const alertSendFailRate = new Rate('alert_send_fail_rate');
const logSendFailRate = new Rate('log_send_fail_rate');
const alertsSent = new Counter('alerts_sent');
const logsSent = new Counter('logs_sent');

// ============================================================================
// Configuration
// ============================================================================
const ALERTSNITCH_URL = __ENV.ALERTSNITCH_URL || 'http://localhost:9567/webhook';
const LOKI_URL = __ENV.LOKI_URL || 'http://localhost:3100';
const SLEEP_DURATION = parseFloat(__ENV.SLEEP_DURATION) || 0.5;

// ============================================================================
// Test Options
// ============================================================================
export const options = {
    scenarios: {
        demo: {
            executor: 'shared-iterations',
            vus: 1,
            iterations: 6, // 6 個 demo 場景
            exec: 'runDemoScenario',
        },
    },
    thresholds: {
        'alert_send_fail_rate': ['rate<0.1'],
        'log_send_fail_rate': ['rate<0.1'],
    },
};

// ============================================================================
// Demo Scenarios - 告警 + 對應的錯誤日誌
// ============================================================================
const DEMO_SCENARIOS = [
    {
        id: 'high-cpu-usage',
        alert: {
            name: 'HighCPUUsage',
            severity: 'critical',
            priority: 'P1',
            service: 'order-service',
            instance: 'order-service-pod-7d8f9c:8080',
            namespace: 'production',
            cluster: 'prod-us-east-1',
            team: 'backend',
            summary: '[CRITICAL] HighCPUUsage on order-service-pod-7d8f9c:8080',
            description: 'CPU usage has exceeded 95% for the last 15 minutes. Service is experiencing high load.',
            runbook_url: 'https://github.com/kubernetes/kubernetes/wiki/Debugging-CPU-Throttling',
            dashboard_url: 'https://grafana.com/grafana/dashboards/315-kubernetes-cluster-monitoring-via-prometheus/',
        },
        logs: [
            { level: 'warn', msg: 'Request queue depth exceeded threshold', queue_depth: 1500, threshold: 1000 },
            { level: 'warn', msg: 'Worker pool exhausted, spawning emergency workers', active_workers: 200, max_workers: 200 },
            { level: 'error', msg: 'Request timeout waiting for worker', request_id: 'req-abc123', wait_time_ms: 5000 },
            { level: 'warn', msg: 'GC pause exceeded threshold', gc_pause_ms: 850, threshold_ms: 200 },
            { level: 'error', msg: 'Circuit breaker OPEN for downstream service', downstream: 'inventory-service', failure_rate: 0.45 },
            { level: 'warn', msg: 'Memory pressure detected, triggering aggressive GC', heap_used_mb: 3800, heap_max_mb: 4096 },
            { level: 'error', msg: 'Request dropped due to load shedding', request_id: 'req-def456', reason: 'queue_full' },
            { level: 'warn', msg: 'Goroutine count abnormally high', goroutine_count: 15000, normal_range: '1000-2000' },
            { level: 'error', msg: 'Database connection pool exhausted', pool_size: 100, waiting_requests: 250 },
            { level: 'critical', msg: 'Service health check degraded', cpu_percent: 98.5, response_time_p99_ms: 8500 },
        ]
    },
    {
        id: 'database-connection-errors',
        alert: {
            name: 'DatabaseConnectionErrors',
            severity: 'critical',
            priority: 'P0',
            service: 'user-service',
            instance: 'user-service-pod-3a2b1c:8080',
            namespace: 'production',
            cluster: 'prod-us-east-1',
            team: 'backend',
            summary: '[CRITICAL] DatabaseConnectionErrors on user-service-pod-3a2b1c:8080',
            description: 'Database connection errors detected. Multiple connection failures to primary database.',
            runbook_url: 'https://github.com/go-sql-driver/mysql/wiki/Connection-Pool-Best-Practices',
            dashboard_url: 'https://grafana.com/grafana/dashboards/7362-mysql-overview/',
        },
        logs: [
            { level: 'warn', msg: 'Database connection latency increased', latency_ms: 500, baseline_ms: 50 },
            { level: 'error', msg: 'Failed to acquire database connection', error: 'connection pool exhausted', pool_size: 50, active: 50 },
            { level: 'error', msg: 'Database query timeout', query: 'SELECT * FROM users WHERE id = ?', timeout_ms: 30000 },
            { level: 'error', msg: 'Lost connection to MySQL server during query', error_code: 2013, host: 'db-primary.internal:3306' },
            { level: 'warn', msg: 'Attempting database reconnection', attempt: 1, max_attempts: 5 },
            { level: 'error', msg: 'Database connection failed', error: 'ECONNREFUSED', host: 'db-primary.internal', port: 3306 },
            { level: 'warn', msg: 'Failing over to read replica', primary: 'db-primary.internal', replica: 'db-replica-1.internal' },
            { level: 'error', msg: 'Read replica also unavailable', error: 'max connections reached', host: 'db-replica-1.internal:3306' },
            { level: 'critical', msg: 'All database connections exhausted, service degraded', healthy_connections: 0, required: 10 },
            { level: 'error', msg: 'Transaction rollback due to connection loss', transaction_id: 'txn-789xyz', affected_rows: 0 },
        ]
    },
    {
        id: 'pod-crash-looping',
        alert: {
            name: 'PodCrashLooping',
            severity: 'critical',
            priority: 'P0',
            service: 'payment-service',
            instance: 'payment-service-pod-9x8y7z:8080',
            namespace: 'production',
            cluster: 'prod-us-east-1',
            team: 'platform',
            summary: '[CRITICAL] PodCrashLooping payment-service-pod-9x8y7z in namespace production',
            description: 'Pod payment-service-pod-9x8y7z is crash looping. Restart count: 5 in last 10 minutes.',
            runbook_url: 'https://kubernetes.io/docs/tasks/debug/debug-application/debug-running-pod/',
            dashboard_url: 'https://grafana.com/grafana/dashboards/6417-kubernetes-cluster-prometheus/',
        },
        logs: [
            { level: 'info', msg: 'Starting payment-service', version: 'v2.3.1', environment: 'production' },
            { level: 'info', msg: 'Loading configuration', config_source: 'configmap/payment-config' },
            { level: 'warn', msg: 'Memory usage climbing rapidly', used_mb: 1800, limit_mb: 2048 },
            { level: 'error', msg: 'Failed to process payment batch', error: 'out of memory allocating 256MB', batch_id: 'batch-001' },
            { level: 'critical', msg: 'FATAL: Out of memory', allocated_mb: 2100, limit_mb: 2048, oom_score: 1000 },
            { level: 'error', msg: 'panic: runtime error: invalid memory address or nil pointer dereference', stack: 'goroutine 1 [running]:\nmain.processPayment()\n\t/app/payment.go:142' },
            { level: 'info', msg: 'Container received SIGTERM, starting graceful shutdown', signal: 'SIGTERM' },
            { level: 'warn', msg: 'Graceful shutdown timeout exceeded, forcing exit', timeout_s: 30 },
            { level: 'error', msg: 'Container killed by OOM killer', exit_code: 137, reason: 'OOMKilled' },
            { level: 'info', msg: 'Container restarting', restart_count: 5, backoff_delay_s: 160 },
        ]
    },
    {
        id: 'service-unavailable',
        alert: {
            name: 'ServiceUnavailable',
            severity: 'warning',
            priority: 'P2',
            service: 'notification-service',
            instance: 'notification-service-pod-abc123:8080',
            namespace: 'production',
            cluster: 'prod-us-east-1',
            team: 'backend',
            summary: '[WARNING] ServiceUnavailable notification-service is not responding to health checks',
            description: 'Service notification-service has failed health checks for the last 5 minutes.',
            runbook_url: 'https://kubernetes.io/docs/tasks/configure-pod-container/configure-liveness-readiness-startup-probes/',
            dashboard_url: 'https://grafana.com/grafana/dashboards/12006-kubernetes-apiserver/',
        },
        logs: [
            { level: 'warn', msg: 'Health check endpoint slow to respond', response_time_ms: 4500, threshold_ms: 1000 },
            { level: 'error', msg: 'Failed to connect to Redis', error: 'ETIMEDOUT', host: 'redis-cluster.internal:6379' },
            { level: 'warn', msg: 'Message queue consumer lag increasing', consumer_group: 'notification-processor', lag: 50000 },
            { level: 'error', msg: 'Failed to send email notification', error: 'SMTP server not responding', smtp_host: 'smtp.internal:587' },
            { level: 'error', msg: 'Push notification delivery failed', provider: 'FCM', error: 'quota exceeded', retry_after_s: 3600 },
            { level: 'warn', msg: 'Rate limiter triggered', endpoint: '/api/v1/notify', requests_per_minute: 10000, limit: 5000 },
            { level: 'error', msg: 'Health check failed', check: 'redis_connectivity', status: 'unhealthy', details: 'connection refused' },
            { level: 'error', msg: 'Health check failed', check: 'kafka_consumer', status: 'unhealthy', details: 'consumer group rebalancing' },
            { level: 'warn', msg: 'Service marked as unhealthy by load balancer', lb: 'prod-nlb', reason: '3 consecutive health check failures' },
            { level: 'error', msg: 'Kubernetes readiness probe failed', probe: 'http-get /health', failure_count: 5 },
        ]
    },
    {
        id: 'high-error-rate',
        alert: {
            name: 'HighErrorRate',
            severity: 'warning',
            priority: 'P1',
            service: 'api-gateway',
            instance: 'api-gateway-pod-xyz789:8080',
            namespace: 'production',
            cluster: 'prod-us-east-1',
            team: 'platform',
            summary: '[WARNING] HighErrorRate for api-gateway has exceeded 5%',
            description: 'Error rate for api-gateway has exceeded 5% threshold. Current rate: 12.3%',
            runbook_url: 'https://sre.google/sre-book/handling-overload/',
            dashboard_url: 'https://grafana.com/grafana/dashboards/11074-node-exporter-for-prometheus-dashboard-en/',
        },
        logs: [
            { level: 'error', msg: 'Upstream service returned 503', upstream: 'order-service', path: '/api/v1/orders', status: 503 },
            { level: 'error', msg: 'Request timeout', method: 'POST', path: '/api/v1/checkout', timeout_ms: 30000, client_ip: '10.0.1.100' },
            { level: 'warn', msg: 'Circuit breaker half-open', service: 'inventory-service', success_rate: 0.6 },
            { level: 'error', msg: 'JWT validation failed', error: 'token expired', user_id: 'user-12345' },
            { level: 'error', msg: 'Rate limit exceeded', client_id: 'partner-api-001', requests: 1200, limit: 1000, window: '1m' },
            { level: 'error', msg: 'Upstream connection reset', upstream: 'payment-service', error: 'connection reset by peer' },
            { level: 'warn', msg: 'High latency detected', p99_latency_ms: 2500, threshold_ms: 1000, path: '/api/v1/search' },
            { level: 'error', msg: 'SSL handshake failed', upstream: 'external-payment-provider', error: 'certificate verify failed' },
            { level: 'error', msg: 'Request body too large', size_mb: 15, max_mb: 10, path: '/api/v1/upload' },
            { level: 'warn', msg: 'Error rate threshold breached', current_rate: 0.123, threshold: 0.05, window: '5m' },
        ]
    },
    {
        id: 'certificate-expiring',
        alert: {
            name: 'CertificateExpiringSoon',
            severity: 'warning',
            priority: 'P2',
            service: 'ingress-controller',
            instance: 'ingress-nginx-controller-abc:443',
            namespace: 'ingress-nginx',
            cluster: 'prod-us-east-1',
            team: 'sre',
            summary: '[WARNING] SSL certificate for api.example.com expires in 7 days',
            description: 'SSL certificate for api.example.com will expire on 2026-01-11. Please renew.',
            runbook_url: 'https://cert-manager.io/docs/troubleshooting/',
            dashboard_url: 'https://grafana.com/grafana/dashboards/11001-cert-manager/',
        },
        logs: [
            { level: 'warn', msg: 'Certificate expiry warning', domain: 'api.example.com', days_until_expiry: 7, expiry_date: '2026-01-11T00:00:00Z' },
            { level: 'info', msg: 'Certificate renewal check initiated', domain: 'api.example.com', issuer: 'Lets Encrypt' },
            { level: 'error', msg: 'Certificate renewal failed', domain: 'api.example.com', error: 'DNS challenge failed', challenge_type: 'dns-01' },
            { level: 'warn', msg: 'Retrying certificate renewal', attempt: 2, max_attempts: 5, domain: 'api.example.com' },
            { level: 'error', msg: 'ACME authorization failed', error: 'rate limit exceeded', domain: 'api.example.com' },
            { level: 'warn', msg: 'TLS secret not updated', secret: 'tls-api-example-com', namespace: 'ingress-nginx', reason: 'renewal pending' },
        ]
    },
];

// 當前場景索引（用於遍歷所有場景）
let scenarioIndex = 0;

// ============================================================================
// Main Test Function
// ============================================================================
export function runDemoScenario() {
    const scenario = DEMO_SCENARIOS[scenarioIndex % DEMO_SCENARIOS.length];
    scenarioIndex++;

    console.log(`\n🔔 [${scenarioIndex}/${DEMO_SCENARIOS.length}] Scenario: ${scenario.id}`);
    console.log('─'.repeat(60));

    const baseTimestamp = Date.now();
    const alertStatus = 'firing';

    // 1. 先發送錯誤日誌到 Loki
    const logsPayload = generateLokiPayload(scenario, baseTimestamp);
    sendLogs(logsPayload, scenario.id);

    sleep(0.3);

    // 2. 發送告警到 AlertSnitch
    const alertPayload = generateAlertPayload(scenario, alertStatus);
    sendAlert(alertPayload);

    sleep(SLEEP_DURATION);
}

// Default function
export default function () {
    runDemoScenario();
}

// ============================================================================
// Alert Payload Generator
// ============================================================================
function generateAlertPayload(scenario, status) {
    const now = Date.now();
    const startOffset = randomIntBetween(5, 35);
    const startsAt = new Date(now - startOffset * 60000);

    let endsAt;
    if (status === 'firing') {
        endsAt = '0001-01-01T00:00:00Z';
    } else {
        const resolvedAfter = randomIntBetween(1, startOffset - 1);
        endsAt = new Date(now - resolvedAfter * 60000).toISOString();
    }

    const labels = {
        alertname: scenario.alert.name,
        severity: scenario.alert.severity,
        priority: scenario.alert.priority,
        instance: scenario.alert.instance,
        namespace: scenario.alert.namespace,
        cluster: scenario.alert.cluster,
        team: scenario.alert.team,
        service: scenario.alert.service,
        job: `${scenario.alert.service}-metrics`,
        environment: 'production',
    };

    const alert = {
        status: status,
        labels: labels,
        annotations: {
            summary: scenario.alert.summary,
            description: scenario.alert.description,
            runbook_url: scenario.alert.runbook_url,
            dashboard_url: scenario.alert.dashboard_url,
        },
        startsAt: startsAt.toISOString(),
        endsAt: endsAt,
        generatorURL: `http://prometheus.example.com/graph?g0.expr=${encodeURIComponent(scenario.alert.name)}`,
        fingerprint: generateFingerprint(labels),
    };

    return {
        receiver: 'alertsnitch-webhook',
        status: status,
        alerts: [alert],
        groupLabels: {
            alertname: scenario.alert.name,
        },
        commonLabels: {
            alertname: scenario.alert.name,
            severity: scenario.alert.severity,
            priority: scenario.alert.priority,
        },
        commonAnnotations: {
            summary: scenario.alert.summary,
            runbook_url: scenario.alert.runbook_url,
        },
        externalURL: 'http://alertmanager.example.com',
        version: '4',
        groupKey: `{alertname="${scenario.alert.name}"}:{severity="${scenario.alert.severity}"}`,
    };
}

// ============================================================================
// Loki Log Payload Generator
// ============================================================================
function generateLokiPayload(scenario, baseTimestamp) {
    const logEntries = scenario.logs.map((log, index) => {
        const offsetMinutes = scenario.logs.length - index;
        const timestamp = baseTimestamp - (offsetMinutes * 60 * 1000) - randomIntBetween(0, 30000);

        const logEntry = {
            level: log.level,
            msg: log.msg,
            service: scenario.alert.service,
            instance: scenario.alert.instance,
            namespace: scenario.alert.namespace,
            cluster: scenario.alert.cluster,
            trace_id: `trace-${randomHex(16)}`,
            timestamp: new Date(timestamp).toISOString(),
        };

        // 複製其他欄位
        for (const key in log) {
            if (key !== 'level' && key !== 'msg') {
                logEntry[key] = log[key];
            }
        }

        return {
            timestamp,
            log: logEntry
        };
    });

    const stream = {
        stream: {
            service_name: scenario.alert.service,
            namespace: scenario.alert.namespace,
            cluster: scenario.alert.cluster,
            pod: scenario.alert.instance.split(':')[0],
            environment: 'production',
            team: scenario.alert.team,
        },
        values: logEntries.map(entry => [
            String(entry.timestamp * 1000000),
            JSON.stringify(entry.log)
        ])
    };

    return { streams: [stream] };
}

// ============================================================================
// HTTP Functions
// ============================================================================
function sendAlert(payload) {
    const res = http.post(ALERTSNITCH_URL, JSON.stringify(payload), {
        headers: { 'Content-Type': 'application/json' },
        tags: { name: 'SendAlert' },
    });

    const alertName = payload.alerts[0].labels.alertname;
    const status = payload.status;

    const success = check(res, {
        'alert status is 200': (r) => r.status === 200,
    });

    alertSendFailRate.add(!success);
    alertsSent.add(1);

    if (success) {
        console.log(`✅ [ALERT] ${status.toUpperCase().padEnd(8)} | ${alertName.padEnd(25)} | sent to AlertSnitch`);
    } else {
        console.log(`❌ [ALERT] ${status.toUpperCase().padEnd(8)} | ${alertName.padEnd(25)} | failed: ${res.status}`);
    }

    return success;
}

function sendLogs(payload, scenarioId) {
    const url = `${LOKI_URL}/loki/api/v1/push`;

    const res = http.post(url, JSON.stringify(payload), {
        headers: { 'Content-Type': 'application/json' },
        tags: { name: 'SendLogs' },
    });

    const logCount = payload.streams[0].values.length;

    const success = check(res, {
        'logs status is 204': (r) => r.status === 204,
    });

    logSendFailRate.add(!success);
    logsSent.add(logCount);

    if (success) {
        console.log(`✅ [LOGS]  ${scenarioId.padEnd(25)} | ${logCount} log entries sent to Loki`);
    } else {
        console.log(`❌ [LOGS]  ${scenarioId.padEnd(25)} | failed: ${res.status} - ${res.body}`);
    }

    return success;
}

// ============================================================================
// Utility Functions
// ============================================================================
function randomIntBetween(min, max) {
    return Math.floor(Math.random() * (max - min + 1)) + min;
}

function randomHex(length) {
    const hexChars = '0123456789abcdef';
    let result = '';
    for (let i = 0; i < length; i++) {
        result += hexChars[Math.floor(Math.random() * hexChars.length)];
    }
    return result;
}

function generateFingerprint(labels) {
    const key = `${labels.alertname}-${labels.instance}-${labels.severity}-${labels.namespace}`;
    let hash = 0;
    for (let i = 0; i < key.length; i++) {
        const char = key.charCodeAt(i);
        hash = ((hash << 5) - hash) + char;
        hash = hash & hash;
    }
    return Math.abs(hash).toString(16).padStart(16, '0').slice(0, 16);
}
