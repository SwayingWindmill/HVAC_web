import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { resolve } from 'node:path';

const root = resolve(process.cwd());
const outputDirectory = resolve(root, 'out/s3-local-thingsboard');
const outputPath = resolve(outputDirectory, 'runtime.yaml');
const bridgeTemplatePath = resolve(root, 'deploy/s3/local-thingsboard/bridge.yaml');

const devices = [
  { slug: 'ahu-01', certificate: 'command-dispatcher-ahu-01', verifierCertificate: 'command-verifier-ahu-01' },
  { slug: 'fcu-02', certificate: 'command-dispatcher-fcu-02', verifierCertificate: 'command-verifier-fcu-02' },
  { slug: 'chiller-03', certificate: 'command-dispatcher-chiller-03', verifierCertificate: 'command-verifier-chiller-03' },
];

const podSecurity = `      automountServiceAccountToken: false
      securityContext:
        runAsNonRoot: true
        runAsUser: 65532
        runAsGroup: 65532
        fsGroup: 65532
        seccompProfile:
          type: RuntimeDefault`;

const containerSecurity = `          securityContext:
            allowPrivilegeEscalation: false
            readOnlyRootFilesystem: true
            capabilities:
              drop: ["ALL"]`;

function dispatcher(device) {
  return `apiVersion: apps/v1
kind: Deployment
metadata:
  name: command-dispatcher-${device.slug}
  namespace: s3-local
  annotations:
    s3.hvac/local-only: "true"
    s3.hvac/formal-certification: "false"
spec:
  replicas: 1
  selector:
    matchLabels:
      app: command-dispatcher-${device.slug}
  template:
    metadata:
      labels:
        app: command-dispatcher-${device.slug}
    spec:
${podSecurity}
      containers:
        - name: command-dispatcher
          image: hvac-s3-local/command-dispatcher:dev
          imagePullPolicy: Never
          env:
            - name: COMMAND_DISPATCHER_WORKER_ID
              value: command-dispatcher-${device.slug}
            - name: COMMAND_DISPATCHER_DIAGNOSTICS_ADDR
              value: ":19088"
            - name: S3_APPROVED_COHORT_FILE
              value: /var/run/s3-local/cohort/approved-cohort.json
            - name: COMMAND_RUNTIME_URL
              value: https://command-service.s3-local.svc.cluster.local:8447
            - name: COMMAND_RUNTIME_SERVER_NAME
              value: command-service.s3-local.svc.cluster.local
            - name: COMMAND_RUNTIME_CLIENT_CERT
              value: /var/run/s3-local/pki/tls.crt
            - name: COMMAND_RUNTIME_CLIENT_KEY
              value: /var/run/s3-local/pki/tls.key
            - name: COMMAND_RUNTIME_SERVER_CA
              value: /var/run/s3-local/pki/ca.crt
            - name: THINGSBOARD_BASE_URL
              value: https://s3-local-thingsboard-bridge.s3-local.svc.cluster.local:8448
            - name: THINGSBOARD_SERVER_NAME
              value: s3-local-thingsboard-bridge.s3-local.svc.cluster.local
            - name: THINGSBOARD_SERVER_CA
              value: /var/run/s3-local/pki/ca.crt
            - name: THINGSBOARD_CREDENTIAL_FILE
              value: /var/run/s3-local/provider/provider-authorization
          ports:
            - name: diagnostics
              containerPort: 19088
          readinessProbe:
            httpGet: { path: /health/ready, port: diagnostics }
            periodSeconds: 2
            timeoutSeconds: 2
            failureThreshold: 30
          livenessProbe:
            httpGet: { path: /health/live, port: diagnostics }
            initialDelaySeconds: 10
            periodSeconds: 10
            timeoutSeconds: 2
          resources:
            requests: { cpu: 50m, memory: 64Mi }
            limits: { cpu: 500m, memory: 256Mi }
${containerSecurity}
          volumeMounts:
            - name: cohort
              mountPath: /var/run/s3-local/cohort
              readOnly: true
            - name: pki
              mountPath: /var/run/s3-local/pki
              readOnly: true
            - name: provider
              mountPath: /var/run/s3-local/provider
              readOnly: true
      volumes:
        - name: cohort
          configMap:
            name: s3-local-thingsboard-cohort-${device.slug}
        - name: pki
          secret:
            secretName: s3-local-thingsboard-dispatcher-${device.slug}-pki
            defaultMode: 288
        - name: provider
          secret:
            secretName: s3-local-thingsboard-provider
            defaultMode: 288
`;
}

function verifier(device) {
  return `apiVersion: apps/v1
kind: Deployment
metadata:
  name: command-verifier-${device.slug}
  namespace: s3-local
  annotations:
    s3.hvac/local-only: "true"
    s3.hvac/formal-certification: "false"
spec:
  replicas: 1
  selector:
    matchLabels:
      app: command-verifier-${device.slug}
  template:
    metadata:
      labels:
        app: command-verifier-${device.slug}
    spec:
${podSecurity}
      containers:
        - name: command-verifier
          image: hvac-s3-local/command-verifier:dev
          imagePullPolicy: Never
          env:
            - name: COMMAND_VERIFIER_WORKER_ID
              value: command-verifier-${device.slug}
            - name: COMMAND_VERIFIER_DIAGNOSTICS_ADDR
              value: ":19089"
            - name: S3_APPROVED_COHORT_FILE
              value: /var/run/s3-local/cohort/approved-cohort.json
            - name: COMMAND_RUNTIME_URL
              value: https://command-service.s3-local.svc.cluster.local:8447
            - name: COMMAND_RUNTIME_SERVER_NAME
              value: command-service.s3-local.svc.cluster.local
            - name: COMMAND_RUNTIME_CLIENT_CERT
              value: /var/run/s3-local/pki/tls.crt
            - name: COMMAND_RUNTIME_CLIENT_KEY
              value: /var/run/s3-local/pki/tls.key
            - name: COMMAND_RUNTIME_SERVER_CA
              value: /var/run/s3-local/pki/ca.crt
            - name: S2_REPORTED_STATE_URL
              value: https://s3-local-thingsboard-bridge.s3-local.svc.cluster.local:8449
            - name: S2_REPORTED_STATE_SERVER_NAME
              value: s3-local-thingsboard-bridge.s3-local.svc.cluster.local
            - name: S2_REPORTED_STATE_CLIENT_CERT
              value: /var/run/s3-local/pki/tls.crt
            - name: S2_REPORTED_STATE_CLIENT_KEY
              value: /var/run/s3-local/pki/tls.key
            - name: S2_REPORTED_STATE_SERVER_CA
              value: /var/run/s3-local/pki/ca.crt
          ports:
            - name: diagnostics
              containerPort: 19089
          readinessProbe:
            httpGet: { path: /health/ready, port: diagnostics }
            periodSeconds: 2
            timeoutSeconds: 2
            failureThreshold: 30
          livenessProbe:
            httpGet: { path: /health/live, port: diagnostics }
            initialDelaySeconds: 10
            periodSeconds: 10
            timeoutSeconds: 2
          resources:
            requests: { cpu: 50m, memory: 64Mi }
            limits: { cpu: 500m, memory: 256Mi }
${containerSecurity}
          volumeMounts:
            - name: cohort
              mountPath: /var/run/s3-local/cohort
              readOnly: true
            - name: pki
              mountPath: /var/run/s3-local/pki
              readOnly: true
      volumes:
        - name: cohort
          configMap:
            name: s3-local-thingsboard-cohort-${device.slug}
        - name: pki
          secret:
            secretName: s3-local-thingsboard-verifier-${device.slug}-pki
            defaultMode: 288
`;
}

const bridgeTemplate = await readFile(bridgeTemplatePath, 'utf8');
const bridge = bridgeTemplate
  .replaceAll('__BRIDGE_CONFIG_RESOURCE__', 's3-local-thingsboard-bridge-config')
  .replaceAll('__BRIDGE_PKI_RESOURCE__', 's3-local-thingsboard-bridge-pki');

const documents = [bridge.trim(), ...devices.flatMap((device) => [dispatcher(device).trim(), verifier(device).trim()])];
await mkdir(outputDirectory, { recursive: true });
await writeFile(outputPath, `${documents.join('\n---\n')}\n`, 'utf8');
console.log(outputPath);
