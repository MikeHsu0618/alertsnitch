import http from 'k6/http';
import { sleep } from 'k6';

export const options = {
    vus: 1,
    iterations: 30,
};

const ALERTSNITCH_URL = 'http://localhost:9567/webhook';

const ALERT_NAMES = [
    "HighCPUUsage",
    "HighMemoryUsage",
    "DiskSpaceLow",
    "NetworkLatency",
    "DatabaseConnectionErrors"
];

const INSTANCES = [
    "server1.example.com",
    "server2.example.com",
    "server3.example.com",
    "app1.example.com",
    "app2.example.com"
];

const SEVERITIES = [
    "warning",
    "critical",
    "info",
];

const PRIORITIES = [
    "P0",
    "P1",
    "P2",
    "P3",
]

export default function () {
    const alertname = randomChoice(ALERT_NAMES);
    const instance = randomChoice(INSTANCES);
    const severity = randomChoice(SEVERITIES);
    const priority = randomChoice(PRIORITIES);

    const now = new Date().getTime();
    const startOffset = randomIntBetween(0, 24 * 60);
    const startsAt = new Date(now - startOffset * 60000);
    const duration = randomIntBetween(30, 180);
    const endsAt = new Date(startsAt.getTime() + duration * 60000);

    const payload = {
        "receiver": "webhook-receiver",
        "status": Math.random() > 0.5 ? "firing" : "resolved",
        "alerts": [
            {
                "status": Math.random() > 0.5 ? "firing" : "resolved",
                "labels": {
                    "alertname": alertname,
                    "severity": severity,
                    "instance": instance,
                    "priority": priority
                },
                "annotations": {
                    "summary": `${alertname} alert on ${instance}`,
                    "description": `The ${alertname} on ${instance} has exceeded threshold for the last ${duration} minutes.`
                },
                "startsAt": startsAt.toISOString(),
                "endsAt": endsAt.toISOString(),
                "generatorURL": `http://prometheus.example.com/graph?g0.expr=${encodeURIComponent(alertname)}%3Ethreshold&g0.tab=1`,
                "fingerprint": randomFingerprint()
            }
        ],
        "groupLabels": {
            "alertname": alertname
        },
        "commonLabels": {
            "alertname": alertname,
            "severity": severity,
            "priority": priority
        },
        "commonAnnotations": {
            "summary": `${alertname} alert`,
            "runbook_url": "http://wiki.example.com/runbook",
        },
        "externalURL": "http://alertmanager.example.com",
        "version": "4",
        "groupKey": `{alertname="${alertname}"}`
    };

    const res = http.post( ALERTSNITCH_URL, JSON.stringify(payload), {
        headers: { 'Content-Type': 'application/json' },
    });

    console.log(`Sent alert: ${alertname} on ${instance} severity ${severity}, startsAt: ${startsAt}, endsAt: ${endsAt} => status ${res.status}`);

    sleep(0.1);
}

function randomChoice(arr) {
    return arr[Math.floor(Math.random() * arr.length)];
}

function randomIntBetween(min, max) {
    return Math.floor(Math.random() * (max - min + 1)) + min;
}

function randomFingerprint() {
    // fingerprint
    const hexChars = '0123456789abcdef';
    let fp = '';
    for (let i = 0; i < 16; i++) {
        fp += hexChars[Math.floor(Math.random() * hexChars.length)];
    }
    return fp;
}
