# neo-blackbox

CCTV 영상 녹화, AI 객체 감지, 이벤트 규칙 평가를 하나로 묶은 백엔드 서버입니다.
Machbase(시계열 DB)에 영상 청크와 감지 데이터를 저장하고, REST API로 조회할 수 있습니다.

## 주요 기능

- **카메라 관리** - RTSP/WebRTC 카메라 등록, 활성화/비활성화, 상태 조회
- **영상 녹화** - FFmpeg를 이용한 RTSP 스트림 녹화 및 청크 단위 DB 저장
- **미디어 서버** - MediaMTX를 통한 RTSP 스트림 관리
- **AI 감지** - blackbox-ai-manager와 연동하여 객체 감지 결과 수집
- **이벤트 규칙** - DSL 기반 규칙 평가 (예: `person > 5 AND car >= 2`)
- **센서 데이터** - 센서 데이터 저장 및 조회
- **웹 UI** - API 테스트용 웹 페이지 내장

## 프로젝트 구조

```
neo-blackbox/
├── cmd/
│   └── neo-blackbox/
│       └── main.go              # 진입점
├── magefile.go                  # 빌드/배포 스크립트 (mage 타겟 정의)
├── mage.go                      # mage 부트스트랩 (go run mage.go 진입점)
├── web/
│   └── index.html               # API 테스트 웹 UI
├── tools/                       # 외부 바이너리 (플랫폼별 서브디렉토리)
│   ├── linux-amd64/             #   ffmpeg, ffprobe, mediamtx, mediamtx.yml
│   ├── linux-arm64/             #   blackbox-ai-manager, blackbox-ai-core
│   ├── darwin-amd64/            #   libonnxruntime.so, config.json 등
│   ├── darwin-arm64/
│   └── windows-amd64/
└── internal/
    ├── ai/                      # AI manager 프로세스 관리
    ├── config/                  # 설정 로드 (config.yaml 포함)
    ├── db/                      # Machbase DB 연동
    ├── dsl/                     # 이벤트 규칙 DSL 파서
    ├── ffmpeg/                  # FFmpeg 프로세스 관리
    ├── logger/                  # 로그 설정
    ├── mediamtx/                # MediaMTX 프로세스/클라이언트 관리
    ├── server/                  # HTTP API 핸들러
    └── watcher/                 # 파일 감시 및 DB 저장
```

## 시작하기

### 필요 사항

- Go 1.25+
- [Machbase](https://machbase.com) (시계열 DB)
- [Mage](https://magefile.org) (빌드 도구, 별도 설치 불필요)

### 설정

`internal/config/config.yaml`을 환경에 맞게 수정합니다.

```yaml
server:
  addr: 0.0.0.0:8000
  camera_dir: "../bin/cameras"   # 카메라 설정 파일 저장 경로
  mvs_dir: "../ai/mvs"           # MVS 파일 저장 경로
  data_dir: "../bin/data"        # 영상 데이터 저장 경로

machbase:
  scheme: "http"
  host: 127.0.0.1
  port: 5654
  timeout_seconds: 30
  api_token: ""                  # Machbase API 토큰 (필요 시)

mediamtx:
  binary: "../tools/mediamtx"          # 비어있으면 외부 서버 사용
  config_file: "../tools/mediamtx.yml" # 비어있으면 바이너리 옆에서 자동 탐색
  host: 127.0.0.1
  port: 9997                           # MediaMTX HTTP API 포트

ffmpeg:
  binary: "../tools/ffmpeg"
  defaults:
    probe_binary: "../tools/ffprobe"
    probe_args:
      - flag: v
        value: "error"
      - flag: select_streams
        value: "v:0"
      - flag: show_entries
        value: "packet=pts_time,duration_time"
      - flag: of
        value: "csv=p=0"

ai:
  binary: "../ai/blackbox-ai-manager"  # 비어있으면 AI 비활성화
  config_file: "../ai/config.json"

log:
  dir: "../logs"                 # 앱 로그 + ffmpeg 로그 디렉토리
  level: "info"                  # debug, info, warn, error
  format: "json"                 # json, text
  output: "both"                 # stdout, file, both
  file:
    filename: "blackbox.log"
    max_size: 100                # MB
    max_backups: 10
    max_age: 30                  # days
    compress: true
```

> **경로 참고**: 모든 상대경로는 **config 파일이 위치한 디렉토리(`config/`) 기준**입니다.
> 예: `../bin/cameras` → `bin/cameras/`, `../ai/mvs` → `ai/mvs/`, `../tools/mediamtx` → `tools/mediamtx`
> 개발 시에는 `internal/config/config.yaml`을 직접 수정하거나, 절대경로로 설정하세요.

### 빌드 및 실행

```bash
# 빌드 (tmp/neo-blackbox 생성)
go run mage.go build

# 실행 (internal/config/config.yaml 사용)
go run mage.go run

# 개발 모드 (go run)
go run mage.go dev

# 커스텀 config로 실행
go run mage.go runWithConfig path/to/config.yaml
go run mage.go devWithConfig path/to/config.yaml

# 테스트
go run mage.go test

# 코드 품질 검사 (fmt + vet + test)
go run mage.go check

# 사용 가능한 타겟 목록
go run mage.go -l
```

### 배포

패키징 시 타겟 플랫폼을 `os-arch` 형식으로 지정합니다.

```bash
# 패키징 (dist/ 폴더에 아카이브 생성)
go run mage.go package linux-amd64
go run mage.go package linux-arm64
go run mage.go package windows-amd64   # .zip 생성

# 기본 서버에 배포 (패키징 + scp)
go run mage.go dp linux-amd64

# G4U 서버에 배포
go run mage.go dpG4u linux-amd64
```

배포 서버 정보는 `.env` 파일로 설정합니다:

```env
DEPLOY_USER=eleven
DEPLOY_HOST=192.168.0.87
DEPLOY_PATH=/blackbox/be/pkg
```

## 패키지 구조

`go run mage.go package` 실행 후 생성되는 구조:

```
neo-blackbox-linux-amd64/
├── bin/
│   ├── neo-blackbox             # 백엔드 바이너리
│   └── web/
│       └── index.html           # 웹 UI (바이너리 실행 위치 기준 탐색)
├── config/
│   └── config.yaml              # 설정 파일 (환경에 맞게 수정 필요)
├── tools/                       # 미디어 도구
│   ├── ffmpeg
│   ├── ffprobe
│   ├── mediamtx
│   └── mediamtx.yml
├── ai/                          # AI 엔진
│   ├── blackbox-ai-manager
│   ├── blackbox-ai-core
│   ├── config.json
│   ├── libonnxruntime.so        # ONNX Runtime 공유 라이브러리
│   ├── models/
│   │   └── *.onnx               # AI 모델 파일
│   └── mvs/                     # MVS 작업 디렉토리 (런타임 생성)
├── logs/                        # 로그 파일 디렉토리 (런타임 생성)
└── README.txt
```

압축 해제 후 실행:

```bash
tar -xzf neo-blackbox-linux-amd64.tar.gz
cd neo-blackbox-linux-amd64

# config/config.yaml 수정 후 실행
./bin/neo-blackbox -config config/config.yaml

# 웹 UI 포함 실행
./bin/neo-blackbox -config config/config.yaml -web

# 환경변수로 오버라이드 (config.yaml 값 무시)
BB_ADDR=0.0.0.0:9000 ./bin/neo-blackbox -config config/config.yaml
BB_MACHBASE_HOST=10.0.0.5 BB_MACHBASE_PORT=5655 ./bin/neo-blackbox -config config/config.yaml
```

> **주의**: `config.yaml`의 상대경로는 **config 파일 위치(`config/`) 기준**입니다.
> 패키지 루트(`neo-blackbox-linux-amd64/`)에서 실행하면 경로가 올바르게 해석됩니다.

## DB 테이블 구조

카메라 생성 시 3개의 TAG 테이블이 자동 생성됩니다. 여러 카메라가 같은 테이블을 공유할 수 있습니다.

### `{table}` — 영상 청크 테이블

```sql
CREATE TAG TABLE IF NOT EXISTS {table} (
    name VARCHAR(128) PRIMARY KEY,
    time DATETIME BASETIME,
    value DOUBLE SUMMARIZED,
    chunk_path VARCHAR(128)
) WITH ROLLUP TAG_PARTITION_COUNT=1, TAG_DATA_PART_SIZE=4194304
```

| 컬럼 | 설명 |
|------|------|
| `name` | 카메라 ID (태그 키) |
| `time` | 청크 시작 시각 |
| `value` | 청크 길이 (초) |
| `chunk_path` | 청크 파일 경로 |

### `{table}_event` — 이벤트 룰 평가 결과 테이블

```sql
CREATE TAG TABLE IF NOT EXISTS {table}_event (
    name VARCHAR(128) PRIMARY KEY,
    time DATETIME BASETIME,
    value DOUBLE,
    expression_text VARCHAR(200),
    used_counts_snapshot JSON
) METADATA (
    camera_id VARCHAR(64),
    rule_id VARCHAR(64),
    rule_name VARCHAR(128)
) TAG_PARTITION_COUNT=1, TAG_DATA_PART_SIZE=4194304
```

| 컬럼 | 설명 |
|------|------|
| `name` | 이벤트 이름 (태그 키) |
| `time` | 이벤트 발생 시각 |
| `value` | 이벤트 값 (1=TRIGGER, 0=RESOLVE 등) |
| `expression_text` | 평가된 DSL 표현식 |
| `used_counts_snapshot` | 평가 시점의 객체 카운트 JSON |
| `camera_id` | 카메라 ID (메타데이터) |
| `rule_id` | 룰 ID (메타데이터) |
| `rule_name` | 룰 이름 (메타데이터) |

### `{table}_log` — AI 감지 로그 테이블

```sql
CREATE TAG TABLE IF NOT EXISTS {table}_log (
    name VARCHAR(128) PRIMARY KEY,
    time DATETIME BASETIME,
    value DOUBLE,
    model_id INTEGER
) METADATA (
    camera_id VARCHAR(64),
    ident VARCHAR(64)
) TAG_PARTITION_COUNT=1, TAG_DATA_PART_SIZE=4194304
```

| 컬럼 | 설명 |
|------|------|
| `name` | `{cameraID}.{ident}` 형식 (예: `camera1.person`) |
| `time` | 감지 시각 |
| `value` | 감지 수량 |
| `model_id` | 사용된 AI 모델 ID |
| `camera_id` | 카메라 ID (메타데이터) |
| `ident` | 감지 객체 종류 (메타데이터) |

---

## REST API

모든 응답은 아래 공통 포맷을 사용합니다:

```json
{
  "success": true,
  "reason": "",
  "elapse": "1.23ms",
  "data": { ... }
}
```

---

### 공통

| Method | Path | 설명 |
|--------|------|------|
| GET | `/api/ping` | 헬스체크 |
| GET | `/api/config` | 앱 설정 조회 |
| POST | `/api/config` | 앱 설정 수정 (server.addr, ai 항목은 읽기 전용) |

---

### 카메라 관리

| Method | Path | 설명 |
|--------|------|------|
| GET | `/api/cameras` | 카메라 목록 조회 |
| POST | `/api/camera` | 카메라 생성 |
| GET | `/api/camera/:id` | 카메라 상세 조회 |
| POST | `/api/camera/:id` | 카메라 수정 |
| DELETE | `/api/camera/:id` | 카메라 삭제 |
| POST | `/api/camera/:id/enable` | 카메라 활성화 (ffmpeg 시작) |
| POST | `/api/camera/:id/disable` | 카메라 비활성화 (ffmpeg 중지) |
| GET | `/api/camera/:id/status` | 카메라 상태 조회 |
| GET | `/api/cameras/health` | 전체 카메라 상태 조회 |
| POST | `/api/camera/:id/test` | RTSP 접속 테스트 |

**POST /api/camera 요청:**
```json
{
  "table": "cam01",
  "name": "cam01",
  "desc": "주차장 카메라",
  "rtsp_url": "rtsp://user:pass@192.168.1.100/stream1",
  "rtsp_path": "",           // 비어있으면 cam-{16자리 hex} 자동 생성
  "model_id": 0,
  "detect_objects": ["person", "car"],
  "save_objects": false,
  "ffmpeg_options": [],
  "server_url": ""           // WebRTC 외부 IP (WSL 등 환경)
}
```

**POST /api/camera 응답 data:**
```json
{ "camera_id": "cam01" }
```

---

### 영상 조회

| Method | Path | 설명 |
|--------|------|------|
| GET | `/api/get_time_range` | 카메라의 녹화 시간 범위 조회 |
| GET | `/api/get_chunk_info` | 특정 시각의 청크 정보 조회 |
| GET | `/api/v_get_chunk` | 청크 바이너리 데이터 반환 (`application/octet-stream`) |
| GET | `/api/get_camera_rollup_info` | 분 단위 롤업 데이터 조회 |
| GET | `/api/data_gaps` | 녹화 누락 구간 조회 |

**GET /api/get_time_range 파라미터:**
```
?tagname={camera_id}
```
**응답 data:**
```json
{
  "camera": "cam01",
  "start": "2024-01-01T00:00:00Z",
  "end": "2024-01-01T01:00:00Z",
  "chunk_duration_seconds": 5.0,
  "fps": 30
}
```

**GET /api/get_chunk_info 파라미터:**
```
?tagname={camera_id}&time={RFC3339 또는 nanoseconds}
```
**응답 data:**
```json
{
  "camera": "cam01",
  "time": "2024-01-01T00:00:05Z",
  "length": 5.123
}
```

**GET /api/v_get_chunk 파라미터:**
```
?tagname={camera_id}&time={RFC3339 또는 nanoseconds 또는 "0"(초기화 세그먼트)}
```

**GET /api/get_camera_rollup_info 파라미터:**
```
?tagname={camera_id}&minutes={분}&start_time={ns}&end_time={ns}
```
**응답 data:**
```json
{
  "camera": "cam01",
  "minutes": 1,
  "start_time_ns": 1700000000000000000,
  "end_time_ns":   1700003600000000000,
  "start": "2024-01-01T00:00:00Z",
  "end":   "2024-01-01T01:00:00Z",
  "rows": [
    { "time": "2024-01-01T00:00:00Z", "sum_length": 60.0 }
  ]
}
```

**GET /api/data_gaps 파라미터:**
```
?camera_id={id}&start_time={RFC3339}&end_time={RFC3339}&interval={초, 기본 5}
```
**응답 data:**
```json
{
  "camera_id": "cam01",
  "start_time": "2024-01-01T00:00:00Z",
  "end_time": "2024-01-01T01:00:00Z",
  "interval": 5,
  "total_gaps": 3,
  "missing_times": ["2024-01-01T00:05:00Z", ...]
}
```

---

### 이벤트 룰

| Method | Path | 설명 |
|--------|------|------|
| GET | `/api/event_rule/:camera_id` | 카메라의 이벤트 룰 목록 조회 |
| POST | `/api/event_rule` | 이벤트 룰 추가 |
| POST | `/api/event_rule/:camera_id/:rule_id` | 이벤트 룰 수정 |
| DELETE | `/api/event_rule/:camera_id/:rule_id` | 이벤트 룰 삭제 |

**POST /api/event_rule 요청:**
```json
{
  "camera_id": "cam01",
  "rule": {
    "rule_id": "rule_001",
    "name": "사람 5명 초과",
    "expression_text": "person > 5 AND car >= 2",
    "record_mode": "EDGE_ONLY",   // "ALL_MATCHES" 또는 "EDGE_ONLY"
    "enabled": true
  }
}
```

---

### 카메라 이벤트 조회

| Method | Path | 설명 |
|--------|------|------|
| GET | `/api/camera_events` | 이벤트 조회 (시간 범위, 페이지네이션) |
| GET | `/api/camera_events/count` | 마지막 조회 이후 신규 이벤트 수 |

**GET /api/camera_events 파라미터:**
```
?start_time={ns}&end_time={ns}
  &camera_id={id}        // 선택 (없으면 전체)
  &event_name={name}     // 선택
  &event_type={type}     // 선택: MATCH, TRIGGER, RESOLVE, ERROR
  &size={100}&page={1}   // 페이지네이션
```
**응답 data:**
```json
{
  "events": [
    {
      "name": "rule_001",
      "time": "2024-01-01T00:00:05Z",
      "value": 1,
      "value_label": "TRIGGER",
      "expression_text": "person > 5",
      "used_counts_snapshot": "{\"person\":6}",
      "camera_id": "cam01",
      "rule_id": "rule_001",
      "rule_name": "사람 5명 초과"
    }
  ],
  "total_count": 42,
  "total_pages": 1
}
```

---

### AI / 감지 객체

| Method | Path | 설명 |
|--------|------|------|
| GET | `/api/models` | AI 모델 목록 조회 (yolov8n~x) |
| GET | `/api/detect_objects` | 감지 가능한 객체 목록 조회 |
| GET | `/api/camera/:id/detect_objects` | 카메라별 감지 객체 조회 |
| POST | `/api/camera/:id/detect_objects` | 카메라별 감지 객체 수정 |
| POST | `/api/ai/result` | AI 감지 결과 수신 (ai-manager → blackbox) |

---

### MVS

| Method | Path | 설명 |
|--------|------|------|
| POST | `/api/mvs/camera` | MVS 카메라 설정 생성 |

---

### 센서 데이터

| Method | Path | 설명 |
|--------|------|------|
| GET | `/api/sensors` | 카메라별 센서 목록 조회 |
| GET | `/api/sensor_data` | 센서 데이터 조회 |

**GET /api/sensor_data 파라미터:**
```
?sensors={id1,id2,...}&start={RFC3339}&end={RFC3339}
```

---

### 기타

| Method | Path | 설명 |
|--------|------|------|
| GET | `/api/tables` | Machbase TAG 테이블 목록 조회(mounted backup DB 제외) |
| POST | `/api/table` | TAG 테이블 생성 |
| POST | `/api/cameras/ping` | IP 주소 ping 테스트 |
| GET | `/api/media/heartbeat` | MediaMTX 상태 확인 |
| POST | `/db/tql` | Machbase TQL 쿼리 프록시 |

**POST /api/cameras/ping 요청:**
```json
{ "ip": "192.168.1.100", "timeout": 3 }
```
