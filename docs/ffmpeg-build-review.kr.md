# FFmpeg/FFprobe 배포 크기 개선 검토 메모

작성일: 2026-09-07

## 배경

Blackbox 설치 패키지에는 `ffmpeg`와 `ffprobe`가 함께 포함된다. 현재 확인한 Linux 설치본에서는 두 실행 파일이 각각 약 145 MB로, bbox 디렉터리 전체 크기에서 가장 큰 비중을 차지한다.

실제 바이너리 다운로드 및 패키징은 `neo-pkg-blackbox`가 아니라 `neo-pkg-bbox`의 릴리스 빌드에서 수행한다. `neo-pkg-blackbox`는 `neo-pkg-bbox` 릴리스 압축 파일을 내려받아 설치한다.

현재 `neo-pkg-bbox/magefile.go`는 Linux와 Windows용으로 BtbN FFmpeg Builds의 `gpl` 정적 빌드를 내려받고, 그 압축 파일에서 `ffmpeg`와 `ffprobe` 실행 파일만 추출한다. 실행 파일만 추출하더라도 각 정적 실행 파일 안에 동일한 FFmpeg 라이브러리와 여러 코덱이 포함되므로 중복과 불필요한 기능이 남는다.

## 검토할 수 있는 방안

### 1. BtbN `gpl-shared` 빌드 사용

현재 사용하는 BtbN 빌드의 `gpl` 정적 변형을 `gpl-shared` 변형으로 바꾼다. `ffmpeg`와 `ffprobe`가 `libavcodec`, `libavformat` 등의 공용 라이브러리를 공유하므로 두 정적 실행 파일에 포함된 코드 중복을 줄일 수 있다.

장점:

- 현재와 거의 같은 FFmpeg 기능 범위를 유지할 수 있다.
- 전용 빌드보다 도입과 유지보수가 단순하다.
- FFmpeg 버전 변경 시 기존 BtbN 릴리스 흐름을 계속 이용할 수 있다.

확인 및 변경 사항:

- `neo-pkg-bbox/magefile.go`에서 shared 릴리스 파일을 선택한다.
- 실행 파일뿐 아니라 Windows DLL과 Linux 공유 라이브러리도 패키징한다.
- Windows에서는 필요한 DLL을 실행 파일과 함께 배치한다.
- Linux에서는 RPATH 또는 실행 환경의 라이브러리 검색 경로를 정한다.
- macOS ARM64는 현재 별도 공급처의 정적 바이너리를 사용하므로 별도 방안을 검토한다.
- 릴리스 압축 파일과 설치 후 디렉터리 크기를 기존 정적 빌드와 비교한다.

이 방안은 중복을 줄이지만 사용하지 않는 코덱과 기능까지 제거하는 방안은 아니다.

### 2. Blackbox 전용 최소 FFmpeg/FFprobe 빌드

Blackbox에서 지원하기로 한 프로토콜, 컨테이너, 코덱, 필터만 활성화하여 FFmpeg를 직접 빌드한다. 지원 코덱이 추가되면 빌드 설정을 갱신하고 bbox 릴리스를 다시 만드는 방식으로 관리한다.

장점:

- 필요하지 않은 코덱과 기능을 제외할 수 있어 가장 큰 크기 감소를 기대할 수 있다.
- Blackbox가 공식적으로 지원하는 미디어 범위와 실제 배포 바이너리를 일치시킬 수 있다.

선행 결정 사항:

- 입력 프로토콜: RTSP, RTP, TCP, UDP, HTTP/HTTPS 등
- 입력 코덱: 예를 들어 H.264, H.265, MJPEG 중 공식 지원 범위
- 출력 형식: 현재 사용하는 DASH/fMP4 및 필요한 보조 형식
- 스트림 복사만 지원할지, 소프트웨어 또는 하드웨어 트랜스코딩도 지원할지
- TLS, 인증, 네트워크 기능과 필요한 외부 라이브러리
- Linux amd64/arm64, Windows amd64/arm64, macOS arm64별 동일 기능 제공 여부

주의 사항:

- Blackbox는 녹화 실행에 `ffmpeg`를 사용하고, 카메라 접속 확인과 패킷 시간 분석에 `ffprobe`를 사용하므로 현재 구조에서는 둘 다 필요하다.
- 사용자가 임의의 FFmpeg 옵션을 지정할 수 있는 기능은 최소 빌드에서 제외한 코덱이나 필터와 충돌할 수 있다. 지원 가능한 옵션의 범위를 문서화하거나 입력 단계에서 검증해야 한다.
- `ffprobe`에도 실제 입력을 분석하는 데 필요한 프로토콜, demuxer, parser, decoder 구성이 포함되어야 한다.
- 라이선스 조건은 최종 활성화 라이브러리와 배포 방식 기준으로 다시 확인해야 한다.

## 권장 검토 순서

1. `gpl-shared` 시제품을 만들어 플랫폼별 설치 크기와 실행 호환성을 측정한다.
2. Blackbox가 공식 지원할 입력 프로토콜, 코덱, 출력 형식 및 사용자 옵션 범위를 확정한다.
3. 확정된 범위를 기준으로 최소 빌드 설정을 만들고 정적 빌드와 shared 빌드의 크기 및 배포 복잡도를 비교한다.
4. 카메라 연결 검사, 녹화 시작/중지, DASH 세그먼트 생성, 이벤트 구간 분석 및 재생을 실제 지원 코덱별로 검증한다.
5. Linux와 Windows를 우선 검증하고, macOS ARM64의 빌드·배포 방식을 별도로 확정한다.

지원 코덱의 수가 많지 않고 새 코덱 지원 시 릴리스를 다시 만들 수 있다면, 장기적으로는 2번 전용 최소 빌드가 패키지 크기와 지원 범위를 가장 명확하게 관리하는 방법이다. 다만 빠른 위험·효과 확인을 위해 1번 shared 빌드를 먼저 측정하는 것이 좋다.

## 참고 자료

- FFmpeg 다운로드 및 플랫폼별 빌드 안내: <https://ffmpeg.org/download.html>
- FFmpeg 플랫폼 빌드 안내: <https://ffmpeg.org/platform.html>
- FFmpeg 프로토콜 설정: <https://ffmpeg.org/ffmpeg-protocols.html>
- FFmpeg 포맷 설정: <https://ffmpeg.org/ffmpeg-formats.html>
- FFmpeg 코덱 설정: <https://ffmpeg.org/ffmpeg-codecs.html>
- BtbN FFmpeg Builds: <https://github.com/BtbN/FFmpeg-Builds>
