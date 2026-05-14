# Blackbox Backend API Specification

## GET /api/tables

Machbase TAG 테이블 목록 조회 (`_event`, `_log` 접미사 테이블 및 mounted backup database 테이블 제외)

Response:
```json
{
    "tables": ["string"]                  // []string - 테이블 이름 목록
}
```

---

## GET /api/models

사용 가능한 AI 모델 목록 조회 (하드코딩)

Response:
```json
{
    "models": {
        "0": "yolov8n.onnx",          // map[string]string - 모델 ID: 모델 파일명
        "1": "yolov8s.onnx",
        "2": "yolov8m.onnx",
        "3": "yolov8l.onnx",
        "4": "yolov8x.onnx"
    }
}
```

---

## GET /api/detect_objects

감지 가능한 객체 목록 조회 (하드코딩)

Response:
```json
{
    "detect_objects": [                   // []string - 객체 이름 목록 (4종)
        "person",
        "car",
        "truck",
        "bus"
    ]
}
```

---

## GET /api/retention/status

데이터 보존(retention) 설정과 실행 상태 조회.

Response:
```json
{
    "config": {
        "enabled": false,
        "keep_hours": 720,
        "start_at_utc": "18:00",
        "interval_hours": 0,
        "consistency_cleanup": true,
        "targets": {
            "database": true,
            "files": true
        }
    },
    "running": false,
    "next_run_at": "2026-04-30T18:00:00Z",
    "last_run": null
}
```

Note:
- `event` 설정은 사용자 입력으로 노출하지 않는 숨김 운영 정책이다. `/api/config` 저장 요청에 `event` 키가 없으면 서버가 기본값을 적용해서 저장한다.
- 기본값은 `require_video=true`, `video_wait_seconds=120`, `video_retry_initial_seconds=1`, `video_retry_max_seconds=5`다.
- `keep_hours`는 보존 기간이다. 프론트엔드는 일 단위 입력도 시간으로 환산해서 저장한다. 예: 주말 테스트에서 6시간만 보존하려면 `keep_hours: 6`.
- `start_at_utc`는 프로그램 기동 후 최초 retention 작업을 시작할 UTC 기준 시각이다. 프론트엔드는 로컬 시각 입력/표시를 UTC로 변환한다.
- `interval_hours`는 최초 실행 이후 반복 실행 주기다. `0`이면 24시간 주기로 실행한다. 예: 2일마다 실행하려면 `interval_hours: 48`.
- `consistency_cleanup`은 retention 실행 마지막에 cutoff 이전 영상 파일 중 DB row가 없는 파일을 정리한다. 기본값은 `true`다.
- retention 대상 테이블은 현재 카메라 설정과 삭제된 카메라 tombstone에 기록된 테이블이다.

---

## POST /api/retention/run

retention을 즉시 실행한다. 자동 실행은 `config.yaml`의 `retention.enabled`, `keep_hours`, `start_at_utc`, `interval_hours` 설정을 따른다.

Request:
```json
{
    "dry_run": true
}
```

Response:
```json
{
    "started_at": "2026-04-30T09:00:00Z",
    "finished_at": "2026-04-30T09:00:01Z",
    "dry_run": true,
    "cutoff": "2026-03-31T09:00:00Z",
    "cutoff_ns": 1774947600000000000,
    "candidate_rows": 0,
    "deleted_files": 0,
    "missing_files": 0,
    "skipped_files": 0,
    "deleted_metadata": 0,
    "tables": []
}
```

Note:
- 영상 파일은 DB row 삭제가 성공한 뒤 삭제한다.
- `{table}`은 `name = camera_id` 기준으로 처리한다.
- `{table}_event`, `{table}_log`는 metadata의 `camera_id`로 tag name을 찾은 뒤 tag name 단위로 처리한다.
- DB row는 있는데 파일이 없는 cutoff 이전 데이터는 DB row를 삭제하고 `missing_files`로 기록한다.
- 파일은 있는데 DB row가 없는 cutoff 이전 영상 파일은 `consistency_cleanup` 단계에서 삭제한다.
- 삭제된 카메라의 데이터와 파일이 모두 없으면 TAG metadata도 삭제한다.
- 삭제된 카메라의 tombstone은 남아 있는데 해당 `{table}`이 DROP된 경우, DB 참조를 복구할 수 없으므로 해당 카메라의 `output_dir`/`archive_dir` 저장 파일 전체를 삭제한다.

---

## POST /api/camera

카메라 생성

Request:
```json
{
    "table": "string",                    // required - 테이블 이름 (여러 카메라가 같은 테이블 공유 가능)
    "name": "string",                     // required - 카메라 ID (고유 식별자)
    "desc": "string",                     // 카메라 설명

    "rtsp_url": "string",                 // RTSP 스트림 URL
    "webrtc_url": "string",               // WebRTC 스트림 URL
    "media_url": "string",                // 미디어 서버 URL

    "model_id": 0,                        // int - AI 모델 ID (기본값: 0)
    "detect_objects": ["string"],         // []string - 감지할 객체 목록
                                          // 예: ["person", "car", "truck", "bus"]

    "save_objects": false,                // bool - {table}_log 테이블에 감지 데이터 저장 여부

    "ffmpeg_command": "string",           // ffmpeg 실행 경로 (선택)
                                          // 빈 값 시 서버 기본값 사용
    "output_dir": "string",               // required - ffmpeg 출력 디렉토리
                                          // 상대경로 시: {data_dir}/{output_dir}
                                          // 절대경로(/로 시작) 시: 그대로 사용
    "archive_dir": "string",              // required - watcher 아카이브 디렉토리
                                          // 상대경로 시: {data_dir}/{archive_dir}
                                          // 절대경로(/로 시작) 시: 그대로 사용

    "ffmpeg_options": [                   // []ReqKV - FFmpeg 옵션 배열
        { "k": "string", "v": "string" }  // k: 옵션명, v: 옵션값 (optional)
    ]
}
```

Response:
```json
{
    "camera_id": "string"                 // 생성된 카메라 ID
}
```

---

## GET /api/camera/:id

카메라 정보 조회

Response:
```json
{
    "Enabled": false,                     // bool - 카메라 활성화 상태
    "table": "string",                    // 테이블 이름
    "name": "string",                     // 카메라 ID
    "desc": "string",                     // 카메라 설명

    "rtsp_url": "string",                 // RTSP 스트림 URL
    "webrtc_url": "string",               // WebRTC 스트림 URL
    "media_url": "string",                // 미디어 서버 URL

    "model_id": 0,                        // int - AI 모델 ID
    "detect_objects": ["string"],         // []string - 감지할 객체 목록
    "save_objects": false,                // bool - 감지 데이터 저장 여부

    "ffmpeg_command": "string",           // ffmpeg 실행 경로
    "output_dir": "string",               // ffmpeg 출력 디렉토리
    "archive_dir": "string",              // watcher 아카이브 디렉토리

    "ffmpeg_options": [                   // []ReqKV - FFmpeg 옵션 배열
        { "k": "string", "v": "string" }
    ],

    "EventRule": [                        // []EventRule - 이벤트 규칙 배열
        {
            "rule_id": "string",          // 규칙 ID
            "name": "string",             // 규칙 이름
            "expression_text": "string",  // DSL 표현식 (예: "person > 5")
            "record_mode": "string",      // 기록 모드: "ALL_MATCHES" | "EDGE_ONLY"
            "enabled": false              // bool - 규칙 활성화 여부
        }
    ]
}
```

---

## POST /api/camera/:id

카메라 정보 수정 (name, table은 변경 불가)

Request:
```json
{
    "enabled": false,                     // bool - 카메라 활성화 상태
    "desc": "string",                     // 카메라 설명

    "rtsp_url": "string",                 // RTSP 스트림 URL
    "webrtc_url": "string",               // WebRTC 스트림 URL
    "media_url": "string",                // 미디어 서버 URL

    "model_id": 0,                        // int - AI 모델 ID
    "detect_objects": ["string"],         // []string - 감지할 객체 목록
    "save_objects": false,                // bool - 감지 데이터 저장 여부

    "ffmpeg_command": "string",           // ffmpeg 실행 경로
    "output_dir": "string",               // ffmpeg 출력 디렉토리
    "archive_dir": "string",              // watcher 아카이브 디렉토리

    "ffmpeg_options": [                   // []ReqKV - FFmpeg 옵션 배열
        { "k": "string", "v": "string" }
    ]
}
```

Note:
- `name`, `table`, `event_rule`은 변경 불가 (기존 값 유지)
- 모든 필드는 optional (binding:"required" 없음)

Response:
```json
{
    "camera_id": "string"                 // 수정된 카메라 ID
}
```

---

## DELETE /api/camera/:id

카메라 삭제

Response:
```json
{
    "name": "string"                      // 삭제된 카메라 이름
}
```

---

## GET /api/camera/:id/detect_objects

특정 카메라의 감지 객체 목록 조회

Response:
```json
{
    "camera_id": "string",                // 카메라 ID
    "detect_objects": ["string"]          // []string - 감지 객체 목록
}
```

---

## POST /api/camera/:id/detect_objects

특정 카메라의 감지 객체 목록 수정

Request:
```json
{
    "detect_objects": ["string"]          // required - []string - 감지 객체 목록
                                          // 예: ["person", "car", "truck", "bus"]
}
```

Response:
```json
{
    "camera_id": "string",                // 카메라 ID
    "detect_objects": ["string"]          // 업데이트된 감지 객체 목록
}
```

---

## POST /api/camera/:id/enable

카메라 활성화 (ffmpeg 프로세스 시작)

Response:
```json
{
    "name": "string",                     // 카메라 이름
    "pid": 0,                             // int - 프로세스 ID
    "status": "string"                    // 상태: "running" | "stopped"
}
```

---

## POST /api/camera/:id/disable

카메라 비활성화 (ffmpeg 프로세스 종료)

Response:
```json
{
    "name": "string",                     // 카메라 이름
    "status": "string"                    // 상태: "stopped"
}
```

---

## GET /api/camera/:id/status

카메라 상태 조회

Response:
```json
{
    "name": "string",                     // 카메라 이름
    "status": "string",                   // 상태: "running" | "stopped"
    "pid": 0,                             // int - 프로세스 ID (running인 경우)
    "started_at": "string",               // 시작 시간 (RFC3339 형식)
    "uptime": "string"                    // 가동 시간 (예: "2h30m15s")
}
```

---

## GET /api/cameras

카메라 목록 조회

Response:
```json
{
    "cameras": [                          // 카메라 목록 배열
        {
            "id": "string",               // 카메라 ID
            "label": "string"             // 카메라 레이블 (현재 id와 동일)
        }
    ]
}
```

---

## GET /api/cameras/health

전체 카메라 상태 조회

Response:
```json
{
    "total": 0,                           // int - 전체 카메라 수
    "running": 0,                         // int - 실행 중인 카메라 수
    "stopped": 0,                         // int - 중지된 카메라 수
    "cameras": [                          // 카메라 상태 배열
        {
            "name": "string",             // 카메라 이름
            "status": "string",           // 상태: "running" | "stopped"
            "pid": 0,                     // int - 프로세스 ID (running인 경우)
            "started_at": "string",       // 시작 시간 (RFC3339 형식)
            "uptime": "string"            // 가동 시간
        }
    ]
}
```

---

## GET /api/media/heartbeat

MediaMTX 미디어 서버 헬스 체크 (config.yaml의 mediamtx 설정 사용)

Response:
```json
{
    "healthy": true,                      // bool - MediaMTX 서버 상태
    "host": "string",                     // MediaMTX 서버 호스트
    "port": 0                             // int - MediaMTX HTTP API 포트
}
```

Note:
- config.yaml의 `mediamtx.host`와 `mediamtx.port` 설정을 사용하여 MediaMTX HTTP API (기본: http://127.0.0.1:9997)에 연결
- 5초 timeout으로 heartbeat 요청 수행
- 응답이 없거나 오류 발생 시 `healthy: false` 반환

---

## GET /api/event_rule/:camera_id

특정 카메라의 이벤트 규칙 목록 조회

Response:
```json
{
    "camera_id": "cam-001",
    "event_rules": [
        {
            "rule_id": "rule-001",
            "name": "혼잡 감지",
            "alias": "congestion",
            "expression_text": "person > 5 AND car >= 2",
            "record_mode": "EDGE_ONLY",
            "enabled": true
        }
    ]
}
```

- `rule_id`: 규칙 고유 식별자
- `name`: 규칙 이름 (표시용)
- `alias`: 규칙 별칭 (영문, 선택)
- `expression_text`: DSL 표현식 (저장 시 소문자로 정규화)
- `record_mode`: `"ALL_MATCHES"` (매 초 기록) | `"EDGE_ONLY"` (상태 변화 시점만 기록)
- `enabled`: 규칙 활성화 여부

---

## POST /api/event_rule

이벤트 규칙 추가

Request:
```json
{
    "camera_id": "cam-001",
    "rule": {
        "rule_id": "rule-001",
        "name": "혼잡 감지",
        "alias": "congestion",
        "expression_text": "person > 5 AND car >= 2",
        "record_mode": "EDGE_ONLY",
        "enabled": true
    }
}
```

- `camera_id`: required
- `rule.rule_id`: required, 카메라 내 고유해야 함
- `rule.expression_text`: required, DSL 표현식. 지원: 산술(`+-*/`), 비교(`> < >= <= != ==`), 논리(`AND OR NOT`), 괄호
- `rule.record_mode`: required, `"ALL_MATCHES"` | `"EDGE_ONLY"`
- `rule.alias`: optional

Response:
```json
{
    "camera_id": "cam-001",
    "rule": {
        "rule_id": "rule-001",
        "name": "혼잡 감지",
        "alias": "congestion",
        "expression_text": "person > 5 and car >= 2",
        "record_mode": "EDGE_ONLY",
        "enabled": true
    }
}
```

---

## POST /api/event_rule/:camera_id/:rule_id

이벤트 규칙 수정 (rule_id는 URL에서 지정, 변경 불가)

Request:
```json
{
    "name": "혼잡 감지 v2",
    "alias": "congestion_v2",
    "expression_text": "person > 10 OR car >= 5",
    "record_mode": "ALL_MATCHES",
    "enabled": true
}
```

Response:
```json
{
    "camera_id": "cam-001",
    "rule": {
        "rule_id": "rule-001",
        "name": "혼잡 감지 v2",
        "alias": "congestion_v2",
        "expression_text": "person > 10 or car >= 5",
        "record_mode": "ALL_MATCHES",
        "enabled": true
    }
}
```

---

## DELETE /api/event_rule/:camera_id/:rule_id

이벤트 규칙 삭제

Response:
```json
{
    "camera_id": "string",                // 카메라 ID
    "rule_id": "string"                   // 삭제된 규칙 ID
}
```

---

## GET /api/camera_events

카메라 이벤트 로그 조회 ({table}_event 테이블)

Query Parameters:
- `camera_id`: optional - 카메라 ID (미지정 시 전체 카메라 조회)
- `start_time`: required - 시작 시간 (Unix nanoseconds)
- `end_time`: required - 종료 시간 (Unix nanoseconds)
- `event_name`: optional - 이벤트 이름 (camera_id.rule_id 형식)
- `event_type`: optional - 이벤트 코드 (`MATCH`, `TRIGGER`, `RESOLVE`, `ERROR`)
- `size`: optional - 페이지당 조회 건수 (기본값: 100)
- `page`: optional - 페이지 번호 (1부터 시작, 기본값: 1)

Response:
```json
{
    "events": [                           // 이벤트 로그 배열
        {
            "name": "string",             // 이벤트 이름 (camera_id.rule_id 형식)
            "time": "string",             // 이벤트 발생 시간 (RFC3339 형식)
            "value": 0.0,                 // float64 - 이벤트 코드
                                          // 2: MATCH, 1: TRIGGER, 0: RESOLVE, -1: ERROR
            "value_label": "string",      // 이벤트 레이블
                                          // "MATCH" | "TRIGGER" | "RESOLVE" | "ERROR"
            "expression_text": "string",  // DSL 표현식
            "used_counts_snapshot": "string",  // JSON 문자열 - 평가 시 사용된 카운트 스냅샷
                                          // 예: "{\"person\":3,\"car\":2}"
            "camera_id": "string",        // 카메라 ID
            "rule_id": "string"           // 규칙 ID
        }
    ]
}
```

Note:
- **ALL_MATCHES 모드**: 조건이 참일 때마다 `value=2` (MATCH) 기록
- **EDGE_ONLY 모드**: 상태 변화 시점만 기록
  - false → true: `value=1` (TRIGGER)
  - true → false: `value=0` (RESOLVE)
- **ERROR**: DSL 평가 오류 시 `value=-1` (ERROR), EDGE_ONLY 상태는 변경 안 됨

---

## GET /api/camera_events/count

마지막 이벤트 조회 이후 새로 발생한 이벤트 개수 반환

- 서버는 `GET /api/camera_events` 호출 시 `end_time` 파라미터를 기록
- 기록된 시간은 `max(이전 기록, end_time)`으로 항상 최신을 유지
- 이 API는 기록된 시간 ~ 현재 시간 사이의 전체 카메라 이벤트 개수를 반환
- 이벤트 조회 API를 한 번도 호출하지 않은 경우 `count: 0` 반환

Response:
```json
{
    "count": 0                            // int - 새 이벤트 개수
}
```

---

## POST /api/cameras/ping

지정한 IP 주소로 ICMP 핑 수행 (카메라 연결성 확인 용도)

Request:
```json
{
    "ip": "192.168.1.141",                // required - 대상 IPv4/IPv6 주소
                                          // (IP 형식이 아니면 400 반환, command injection 방지)
    "timeout": 3                          // int - 대기시간(초)
                                          // 범위: 1~5, 기본값: 5, 그 외 값은 5로 보정
}
```

Response:
```json
{
    "ip": "192.168.1.141",                // 대상 IP
    "alive": true,                        // bool - 응답 성공 여부
    "latency": "12.345ms",                // time.Duration 문자열 (alive=true 일 때)
    "output": "string",                   // ping 명령 표준출력/에러 (trim 처리)
    "error": "string"                     // alive=false 일 때 에러 메시지
}
```

Note:
- 서버 호스트의 `ping` 명령을 실행 (OS별 옵션 자동 분기: Windows `-n 1 -w <ms>`, macOS `-c 1 -W <ms>`, Linux `-c 1 -W <sec>`)
- 요청 컨텍스트 타임아웃도 동일하게 `timeout` 초로 설정
- IP는 `net.ParseIP`로 검증, 통과하지 못하면 400

---

## GET /api/camera/ping

`POST /api/cameras/ping` 과 동일 동작. 쿼리스트링 방식.

Query Parameters:
- `ip`: required - 대상 IPv4/IPv6 주소
- `timeout`: optional - 대기시간(초), 범위 1~5, 기본값 5

Example:
```
GET /api/camera/ping?ip=192.168.1.141&timeout=3
```

Response: `POST /api/cameras/ping` 과 동일

---

## POST /api/ai/result

AI 감지 결과 업로드. `save_objects=true`인 카메라는 `{table}_log`에 감지 count를 저장하고, event rule 결과는 영상 chunk가 확인된 뒤 `{table}_event`에 저장한다.

Request:
```json
{
    "camera_id": "string",                // required - 카메라 ID
    "model_id": 0,                        // int - AI 모델 ID
    "timestamp": 0,                       // int64 - 밀리초 단위 타임스탬프
                                          // (0은 유효하지 않음)
    "detections": {                       // map[string]int - 객체별 감지 카운트
        "person": 0,                      // 예: person 객체 0개 감지
        "car": 0                          // 예: car 객체 0개 감지
    },
    "total_objects": 0                    // int - 전체 감지된 객체 수
}
```

Response:
```json
null                                      // 응답 본문 없음 (success: true만 확인)
```

Note:
- `event.require_video=true`이면 API 응답 후 백그라운드에서 최대 `event.video_wait_seconds` 동안 해당 timestamp의 영상 chunk row와 파일을 확인한다.
- `ALL_MATCHES`와 `EDGE_ONLY` 이벤트는 카메라별 내부 큐에서 순차적으로 재시도/저장한다. 연속 발생 시 API 요청마다 대기 goroutine을 만들지 않는다.
- 큐 크기는 카메라별 16,384개다. 특정 카메라의 video 지연이나 queue full은 다른 카메라 queue에 영향을 주지 않는다. queue full 로그는 반복 시 일정 간격으로 누적 요약한다.
- 해당 timestamp의 video는 없고 그 이후 timestamp의 video row와 파일이 확인되면 `video_gap_after_timestamp`로 즉시 drop한다.
- `EDGE_ONLY`의 trigger/resolve 판정은 큐에 넣기 전 AI result 처리 시점에 수행한다. 이후 video 확인 실패로 drop되면 해당 trigger/resolve row는 저장되지 않는다.
- 확인되면 event rule 결과를 `{table}_event`에 저장한다.
- 확인되지 않으면 event는 저장하지 않고 `blackbox.log`에 drop 로그를 남긴다.
- 감지 count 저장(`{table}_log`)은 기존처럼 API 처리 시점에 즉시 수행한다.

---

## GET /api/get_time_range?tagname={tagname}

카메라의 비디오 데이터 시간 범위 조회

Response:
```json
{
    "camera": "string",                   // 카메라 이름
    "start": "string",                    // 시작 시간 (RFC3339 형식)
    "end": "string",                      // 종료 시간 (RFC3339 형식)
    "chunk_duration_seconds": 0.0,        // float64 - 청크 길이 (초)
    "fps": 0                              // int - 프레임 레이트
}
```

---

## GET /api/get_chunk_info?tagname={tagname}&time={time}

특정 시간의 비디오 청크 정보 조회

Response:
```json
{
    "camera": "string",                   // 카메라 이름
    "time": "string",                     // 청크 시간 (RFC3339 형식)
    "length": 0,                          // int - 청크 크기 (바이트)
    "sign": 0                             // int - 서명 값
}
```

Note:
- 청크 검색은 요청된 시간(`time`)이 청크의 시간 범위 내에 포함되는지 확인
- 검색 조건: `chunk.time <= requested_time <= chunk.time + chunk.length`
- `chunk.length`는 청크의 길이(초 단위)를 나타내며, 나노초로 변환하여 계산
- 조건을 만족하는 첫 번째 청크를 반환

---

## GET /api/v_get_chunk?tagname={tagname}&time={time}

특정 시간의 비디오 청크 다운로드 (바이너리)

Response:
```
binary                                    // 비디오 청크 바이너리 데이터
```

Note:
- `time=0` 또는 `time=init`: 초기화 세그먼트(init segment) 반환
- 그 외: 요청 시간을 포함하는 청크 검색 후 바이너리 데이터 반환
- 청크 검색 로직은 `/api/get_chunk_info`와 동일

---

## GET /api/get_camera_rollup_info?tagname={tagname}&minutes={minutes}&start_time={start_time}&end_time={end_time}

카메라 비디오 데이터 롤업 정보 조회 (시간대별 집계)

Response:
```json
{
    "camera": "string",                   // 카메라 이름
    "minutes": 0,                         // int - 집계 단위 (분)
    "start_time_ns": 0,                   // int64 - 시작 시간 (나노초)
    "end_time_ns": 0,                     // int64 - 종료 시간 (나노초)
    "start": "string",                    // 시작 시간 (RFC3339 형식)
    "end": "string",                      // 종료 시간 (RFC3339 형식)
    "rows": [                             // 시간대별 데이터 배열
        {
            "time": "string",             // 시간 (RFC3339 형식)
            "sum_length": 0.0             // float64 - 해당 시간대 총 데이터 크기
        }
    ]
}
```

---

## GET /api/data_gaps?camera_id={camera_id}&start_time={start_time}&end_time={end_time}&interval={interval}

데이터 Gap 분석 - 지정된 간격으로 빠진 시간대 조회

Query Parameters:
- `camera_id` (required): 카메라 ID (카메라 설정 파일에서 테이블 이름 자동 조회)
- `start_time` (required): 시작 시간 (RFC3339 형식, 예: "2024-01-01T10:00:00Z")
- `end_time` (required): 종료 시간 (RFC3339 형식, 예: "2024-01-01T11:00:00Z")
- `interval` (optional): 조회 간격 (초 단위, 기본값: 5)

Response:
```json
{
    "camera_id": "string",                // 카메라 ID
    "start_time": "string",               // 시작 시간 (RFC3339 형식)
    "end_time": "string",                 // 종료 시간 (RFC3339 형식)
    "interval": 5,                        // int - 조회 간격 (초)
    "total_gaps": 0,                      // int - 빠진 시간대 개수
    "missing_times": ["string"]           // []string - 빠진 시간대 목록 (RFC3339 형식)
}
```

Note:
- 카메라 설정 파일(JSON)에서 테이블 이름을 자동으로 가져옴
- `interval` 파라미터로 지정된 간격(초)으로 rollup 쿼리를 실행하여 실제 데이터를 조회
- 시작~종료 시간 사이의 예상 시간대(지정 간격)를 생성
- 실제 데이터에 없는 시간대만 `missing_times`에 반환
- 빈 배열(`[]`)은 모든 시간대에 데이터가 존재함을 의미
- `interval`을 생략하거나 0 이하 값을 입력하면 기본값 5초 사용

**타임존 처리**:
- 모든 시간은 **RFC3339 형식**으로 처리됨 (타임존 포함)
- 입력 예시:
  - `2024-01-01T10:00:00Z` (UTC 시간)
  - `2024-01-01T19:00:00+09:00` (한국 시간, 서버에서 자동으로 UTC로 변환)
- 출력은 항상 **UTC (Z 접미사)**로 반환됨
- 프론트엔드에서는 UTC로 전송하거나, 로컬 타임존을 명시하여 전송 가능
- JavaScript: `new Date().toISOString()` 사용 시 자동으로 UTC 변환됨

---

## GET /api/sensors?tagname={tagname}

카메라의 센서 목록 조회

Response:
```json
{
    "camera": "string",                   // 카메라 이름
    "sensors": [                          // 센서 목록 배열
        {
            "id": "string",               // 센서 ID (예: "sensor-1")
            "label": "string"             // 센서 레이블 (예: "Sensor 1")
        }
    ]
}
```

---

## GET /api/sensor_data?sensors={sensors}&start={start}&end={end}

센서 데이터 조회

- `sensors`: 쉼표로 구분된 센서 ID 목록 (예: "sensor-1,sensor-2")
- `start`: 시작 시간 (RFC3339 형식)
- `end`: 종료 시간 (RFC3339 형식)

Response:
```json
{
    "sensors": ["string"],                // []string - 조회된 센서 ID 목록
    "samples": [                          // 시간별 샘플 데이터 배열
        {
            "time": "string",             // 샘플 시간 (RFC3339 형식)
            "values": {                   // map[string]float64 - 센서별 값
                "sensor-1": 0.0           // 센서 ID를 키로 하는 측정값
            }
        }
    ]
}
```

---

## POST /api/mvs/camera

MVS 카메라 설정 파일 생성 (.mvs 파일)

Request:
```json
{
    "camera_id": "string",                // required - 카메라 ID
    "camera_url": "string",               // required - 카메라 URL
    "model_id": 0,                        // int - AI 모델 ID
    "detect_objects": ["string"]          // []string - 감지할 객체 목록
}
```

Response:
```json
{
    "camera_id": "string",                // 카메라 ID
    "mvs_path": "string"                  // 생성된 .mvs 파일 경로
}
```
