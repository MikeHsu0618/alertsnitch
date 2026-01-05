import http from 'k6/http';
import { check, sleep } from 'k6';
import { Rate, Trend } from 'k6/metrics';

// ============================================================================
// Custom Metrics
// ============================================================================
const alertSendFailRate = new Rate('alert_send_fail_rate');
const alertSendDuration = new Trend('alert_send_duration');

// ============================================================================
// Configuration (支援環境變數)
// ============================================================================
const ALERTSNITCH_URL = __ENV.ALERTSNITCH_URL || 'http://localhost:9567/webhook';
const SLEEP_DURATION = parseFloat(__ENV.SLEEP_DURATION) || 0.1;

// ============================================================================
// Test Scenarios
// ============================================================================
export const options = {
    scenarios: {
        // 預設場景：快速測試
        smoke: {
            executor: 'shared-iterations',
            vus: 1,
            iterations: 30,
            exec: 'smokeTest',
        },
        // 負載測試（需要 -e SCENARIO=load 啟用）
        // load: {
        //     executor: 'ramping-vus',
        //     startVUs: 0,
        //     stages: [
        //         { duration: '30s', target: 5 },
        //         { duration: '1m', target: 10 },
        //         { duration: '30s', target: 0 },
        //     ],
        //     exec: 'loadTest',
        // },
    },
    thresholds: {
        'http_req_duration': ['p(95)<500'],
        'alert_send_fail_rate': ['rate<0.01'],
    },
};

// ============================================================================
// Test Data - Alert Names with realistic descriptions
// ============================================================================
const ALERTS = [
    { name: "HighCPUUsage", description: "CPU usage has exceeded {threshold}% for the last {duration} minutes" },
    { name: "HighMemoryUsage", description: "Memory usage is above {threshold}% threshold" },
    { name: "DiskSpaceLow", description: "Disk space is below {threshold}% on {instance}" },
    { name: "NetworkLatency", description: "Network latency to {instance} exceeds {threshold}ms" },
    { name: "DatabaseConnectionErrors", description: "Database connection errors detected on {instance}" },
    { name: "PodCrashLooping", description: "Pod {pod} is crash looping in namespace {namespace}" },
    { name: "ServiceUnavailable", description: "Service {service} is not responding to health checks" },
    { name: "CertificateExpiringSoon", description: "SSL certificate for {instance} expires in {duration} days" },
    { name: "HighErrorRate", description: "Error rate for {service} has exceeded {threshold}%" },
    { name: "KubernetesNodeNotReady", description: "Kubernetes node {instance} is in NotReady state" },
];

const NAMESPACES = ["default", "production", "staging", "monitoring", "kube-system"];
const CLUSTERS = ["prod-us-east-1", "prod-eu-west-1", "staging-us-west-2", "dev-local"];
const ENVIRONMENTS = ["production", "staging", "development"];
const TEAMS = ["platform", "backend", "frontend", "data", "sre"];
const SERVICES = ["api-gateway", "user-service", "order-service", "payment-service", "notification-service"];

const INSTANCES = [
    "server1.example.com:9090",
    "server2.example.com:9090",
    "server3.example.com:9090",
    "app1.example.com:8080",
    "app2.example.com:8080",
    "db-primary.example.com:5432",
    "db-replica.example.com:5432",
    "cache-01.example.com:6379",
];

const SEVERITIES = ["info", "warning", "critical"];
const PRIORITIES = ["P0", "P1", "P2", "P3"];

// ============================================================================
// Test Functions
// ============================================================================
export function smokeTest() {
    const payload = generateAlertPayload();
    sendAlert(payload);
    sleep(SLEEP_DURATION);
}

export function loadTest() {
    const payload = generateAlertPayload({ multipleAlerts: true });
    sendAlert(payload);
    sleep(SLEEP_DURATION);
}

// Default function (for backwards compatibility)
export default function () {
    smokeTest();
}

// ============================================================================
// Alert Generation
// ============================================================================
function generateAlertPayload(opts = {}) {
    const { multipleAlerts = false } = opts;
    
    const alertCount = multipleAlerts ? randomIntBetween(1, 5) : 1;
    const alerts = [];
    
    // 決定整體狀態（所有 alerts 共用）
    const status = Math.random() > 0.3 ? "firing" : "resolved";
    
    for (let i = 0; i < alertCount; i++) {
        alerts.push(generateSingleAlert(status));
    }
    
    // 使用第一個 alert 的資訊作為 common labels
    const firstAlert = alerts[0];
    
    return {
        receiver: "alertsnitch-webhook",
        status: status,
        alerts: alerts,
        groupLabels: {
            alertname: firstAlert.labels.alertname,
        },
        commonLabels: {
            alertname: firstAlert.labels.alertname,
            severity: firstAlert.labels.severity,
            priority: firstAlert.labels.priority,
        },
        commonAnnotations: {
            summary: `${firstAlert.labels.alertname} alert`,
            runbook_url: `https://wiki.example.com/runbooks/${firstAlert.labels.alertname}`,
        },
        externalURL: "http://alertmanager.example.com",
        version: "4",
        groupKey: `{alertname="${firstAlert.labels.alertname}"}:{severity="${firstAlert.labels.severity}"}`,
    };
}

function generateSingleAlert(status) {
    const alertDef = randomChoice(ALERTS);
    const alertname = alertDef.name;
    const instance = randomChoice(INSTANCES);
    const severity = randomChoice(SEVERITIES);
    const priority = mapSeverityToPriority(severity);
    const namespace = randomChoice(NAMESPACES);
    const cluster = randomChoice(CLUSTERS);
    const environment = randomChoice(ENVIRONMENTS);
    const team = randomChoice(TEAMS);
    const service = randomChoice(SERVICES);
    
    const now = Date.now();
    const durationMinutes = randomIntBetween(5, 120);
    
    // 時間邏輯：
    // - firing: startsAt 在過去，endsAt 設為 "0001-01-01T00:00:00Z"（表示尚未結束）
    // - resolved: startsAt 在過去，endsAt 也在過去但在 startsAt 之後
    const startOffset = randomIntBetween(5, 60); // 5-60 分鐘前開始
    const startsAt = new Date(now - startOffset * 60000);
    
    let endsAt;
    if (status === "firing") {
        endsAt = "0001-01-01T00:00:00Z";
    } else {
        // resolved: 在 startsAt 之後，但在現在之前
        const resolvedAfter = randomIntBetween(1, startOffset - 1);
        endsAt = new Date(now - resolvedAfter * 60000).toISOString();
    }
    
    const labels = {
        alertname: alertname,
        severity: severity,
        priority: priority,
        instance: instance,
        namespace: namespace,
        cluster: cluster,
        environment: environment,
        team: team,
        service: service,
        job: `${service}-metrics`,
    };
    
    // 基於 labels 生成一致的 fingerprint
    const fingerprint = generateFingerprint(labels);
    
    // 生成 description，替換佔位符
    const description = alertDef.description
        .replace('{threshold}', randomIntBetween(80, 95))
        .replace('{duration}', durationMinutes)
        .replace('{instance}', instance)
        .replace('{namespace}', namespace)
        .replace('{pod}', `${service}-${randomHex(8)}`)
        .replace('{service}', service);
    
    return {
        status: status,
        labels: labels,
        annotations: {
            summary: `[${severity.toUpperCase()}] ${alertname} on ${instance}`,
            description: description,
            runbook_url: `https://wiki.example.com/runbooks/${alertname}`,
            dashboard_url: `https://grafana.example.com/d/${service}?var-instance=${instance}`,
        },
        startsAt: startsAt.toISOString(),
        endsAt: typeof endsAt === 'string' ? endsAt : endsAt.toISOString(),
        generatorURL: `http://prometheus.example.com/graph?g0.expr=${encodeURIComponent(alertname)}&g0.tab=1`,
        fingerprint: fingerprint,
    };
}

// ============================================================================
// HTTP Request
// ============================================================================
function sendAlert(payload) {
    const startTime = Date.now();
    
    const res = http.post(ALERTSNITCH_URL, JSON.stringify(payload), {
        headers: { 
            'Content-Type': 'application/json',
            'X-K6-Test': 'true',
        },
        tags: { name: 'SendAlert' },
    });
    
    const duration = Date.now() - startTime;
    alertSendDuration.add(duration);
    
    const success = check(res, {
        'status is 200': (r) => r.status === 200,
        'response time < 500ms': (r) => r.timings.duration < 500,
    });
    
    alertSendFailRate.add(!success);
    
    // 簡潔的 log 輸出
    const firstAlert = payload.alerts[0];
    console.log(
        `[${payload.status.toUpperCase().padEnd(8)}] ` +
        `${firstAlert.labels.alertname.padEnd(25)} | ` +
        `${firstAlert.labels.severity.padEnd(8)} | ` +
        `${firstAlert.labels.instance.padEnd(30)} | ` +
        `alerts: ${payload.alerts.length} | ` +
        `status: ${res.status}`
    );
    
    return res;
}

// ============================================================================
// Utility Functions
// ============================================================================
function randomChoice(arr) {
    return arr[Math.floor(Math.random() * arr.length)];
}

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

// 基於 labels 生成一致的 fingerprint（簡化版 hash）
function generateFingerprint(labels) {
    const key = `${labels.alertname}-${labels.instance}-${labels.severity}-${labels.namespace}`;
    let hash = 0;
    for (let i = 0; i < key.length; i++) {
        const char = key.charCodeAt(i);
        hash = ((hash << 5) - hash) + char;
        hash = hash & hash; // Convert to 32bit integer
    }
    return Math.abs(hash).toString(16).padStart(16, '0').slice(0, 16);
}

// 根據 severity 映射合理的 priority
function mapSeverityToPriority(severity) {
    switch (severity) {
        case 'critical': return randomChoice(['P0', 'P1']);
        case 'warning': return randomChoice(['P1', 'P2']);
        case 'info': return randomChoice(['P2', 'P3']);
        default: return 'P2';
    }
}
