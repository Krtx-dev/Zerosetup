// Types mirroring the Go API's JSON contracts (internal/api, pkg/planschema,
// internal/engine). Keep in sync with those packages.

export type Kind = 'USER_OVERRIDE' | 'DETECTED' | 'INFERRED' | 'DEFAULT' | 'UNSUPPORTED'
export type Certainty = 'declared' | 'high' | 'medium' | 'low'

export interface Evidence {
  file?: string
  detail: string
  signal?: string
}

export interface Decision {
  field: string
  value: string
  kind: Kind
  certainty: Certainty
  reason: string
  evidence?: Evidence[]
}

export interface Command {
  argv: string[]
  workdir?: string
}

export interface Step {
  id: string
  name: string
  stage: 'install' | 'build'
  command: Command
  needsNetwork: boolean
  timeoutSec: number
  reason: string
}

export interface EnvVar {
  name: string
  value?: string
  secret?: boolean
  required?: boolean
  kind: Kind
  reason?: string
}

export interface ExistingConfig {
  kind: 'dockerfile' | 'compose' | 'devcontainer'
  file: string
  used: boolean
  reason: string
  findings?: string[]
}

export interface Plan {
  schemaVersion: string
  source: {
    kind: 'github' | 'local'
    repository?: string
    url?: string
    ref?: string
    commit?: string
    subdir?: string
    contentHash: string
  }
  stack: {
    language: string
    framework: string
    runtime: string
    runtimeVersion: string
    packageManager: string
    packageManagerVersion?: string
  }
  image: { reference: string }
  steps: Step[]
  start: Command
  ports: { name: string; containerPort: number; protocol: string }[]
  env: EnvVar[]
  readiness: { type: string; port: number; path?: string; expectStatus?: number[]; timeoutSec: number }[]
  resources: { cpus: number; memoryMB: number; pidsLimit: number; logMaxSizeMB: number; startTimeoutSec: number }
  network: { publishHost: string; installOutbound: boolean; runtimeOutbound: boolean; outboundFiltered: boolean }
  filesystem: { hostMounts: boolean; readonlyRootfs: boolean }
  decisions: Decision[]
  warnings: { code: string; message: string; file?: string }[]
  conflicts: { field: string; signals: string[]; resolution: string }[]
  existingConfigs: ExistingConfig[]
  compatibility: { runnable: boolean; level: 'supported' | 'experimental' | 'unsupported'; reasons?: string[] }
  overrides: string[]
  services?: ManagedService[]
}

export interface ManagedService {
  kind: 'postgres' | 'redis'
  image: string
  alias: string
  port: number
  user: string
  dataDir: string
  readyCommand: string[]
  reason: string
}

export interface Overrides {
  port?: number
  runtimeVersion?: string
  script?: string
  readinessPath?: string
  readyTimeoutSec?: number
  env?: Record<string, string>
  secretNames?: string[]
  memoryMB?: number
  cpus?: number
  mode?: 'dev' | 'production'
  workspace?: string
  services?: ('postgres' | 'redis')[]
}

export interface InspectRequest {
  source: string
  ref?: string
  overrides: Overrides
}

export interface InspectResponse {
  planHash: string
  planShortHash: string
  plan: Plan
  policyViolations: { rule: string; message: string }[] | null
  detectors: { id: string; framework: string; runnable: boolean; unsupported?: string[] }[] | null
  services: { kind: string; file: string; evidence: string }[] | null
  source: { kind: string; repository?: string; url?: string; ref?: string; commit?: string; dir: string; cacheHit: boolean }
  manifests: FileHash[] | null
  files: number
  timingMs: Record<string, number>
}

export interface FileHash {
  file: string
  sha256: string
  lockfile: boolean
}

export type SessionState =
  | 'CREATED' | 'INSPECTING' | 'PLANNED' | 'AWAITING_APPROVAL' | 'PREPARING' | 'BUILDING' | 'STARTING'
  | 'VERIFYING' | 'READY' | 'FAILED' | 'STOPPING' | 'STOPPED' | 'REMOVING' | 'REMOVED' | 'CANCELLED' | 'UNKNOWN'

export type Milestone =
  | 'IMAGE_READY' | 'DEPENDENCIES_INSTALLED' | 'BUILD_SUCCESS' | 'PROCESS_STARTED'
  | 'PORT_LISTENING' | 'HTTP_READY' | 'APPLICATION_HEALTHY'

export interface ApiError {
  code: string
  stage?: string
  summary: string
  detail?: string
  suggestion?: string
}

export interface Session {
  id: string
  state: SessionState
  source: string
  repository: string
  commit?: string
  framework: string
  runtime: string
  planHash: string
  approvedAt?: string
  url?: string
  hostPort?: number
  containerId?: string
  imageDigest?: string
  milestones: Partial<Record<Milestone, string>> | null
  error?: ApiError & { cause?: string }
  runnerPid?: number
  createdAt: string
  updatedAt: string
  attempt: number
  prevPlanHash?: string
}

export interface Listener {
  addr: string
  port: number
  loopback: boolean
}

export interface Repair {
  id: string
  title: string
  explanation: string
  changes: { override: string; key?: string; from: string; to: string }[]
  impact: string
}

export interface Diagnostic {
  id: string
  category: string
  severity: 'error' | 'warning' | 'info'
  summary: string
  evidence: string[] | null
  likelyCause: string
  proposedResolution: string
  confidence: 'high' | 'medium' | 'low'
  stage: string
  docs?: string
  repair?: Repair
}

export interface StepRecord {
  id: string
  name: string
  stage: string
  exitCode: number
  durationMs: number
  logFile: string
  skipped?: boolean
  skipReason?: string
}

export interface Facts {
  zerosetupVersion: string
  input: string
  overrides: Overrides
  manifests: FileHash[] | null
  image?: { reference: string; id: string; digest: string; os: string; arch: string; pulled: boolean }
  engine?: { backend: string; serverVersion: string; os: string; arch: string; cgroupVersion: string; endpoint: string }
  pack?: { files: number; bytes: number; skipped?: string[] }
  steps?: StepRecord[]
  readiness?: {
    path: string
    attempts: number
    durationMs: number
    listeners: Listener[] | null
    lastStatus: number
    lastError?: string
    httpReady: boolean
    healthy: boolean
    failedReason?: string
  }
  diagnostics?: Diagnostic[]
  cache?: { volume: string; volumeExisted: boolean; depsKey: string; depsReused: boolean; installOk: boolean; reason: string }
  repairsApplied?: string[]
  services?: { kind: string; containerId: string; image: string; digest?: string; readyMs: number }[]
}

export interface SessionDetail {
  session: Session
  facts: Facts
  plan: Plan
  planShortHash: string
  executingHere: boolean
}

export interface SessionEvent {
  seq: number
  sessionId: string
  time: string
  type: 'state' | 'stage' | 'milestone' | 'log' | 'warning' | 'error' | 'progress' | 'diagnostic'
  stage?: string
  message: string
  data?: Record<string, unknown>
}

export interface DoctorCheck {
  name: string
  status: 'ok' | 'warn' | 'fail'
  detail: string
  suggestion?: string
}

export interface Stats {
  cpuPercent: number
  memoryBytes: number
  memoryLimit: number
  pids: number
  netRxBytes: number
  netTxBytes: number
  sampledAt: string
}

export interface Metrics {
  timings: { attempt: number; name: string; millis: number }[] | null
  usage?: Stats
  usageUnavailable?: string
}

export interface CacheStatus {
  volumes: string[] | null
  sourceCheckouts: number
  sourceBytes: number
  sourceRoot: string
}

export interface Config {
  home: string
  database: string
  sourceCache: string
  sessionLogs: string
  version: string
  defaults: Record<string, string | number>
  fixedContainerEnv: string[]
}
