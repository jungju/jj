# Playground Plan

샘플 Go CLI에 `--name` 옵션을 추가한다.

## 목표

- `go run ./cmd/hello --name Jungju`를 실행하면 `Hello, Jungju!`를 출력한다.
- `--name`이 없으면 기존 기본값인 `Hello, world!`를 출력한다.
- greeting 생성 로직은 테스트 가능하게 유지한다.

## 기대 작업

- `cmd/hello`의 CLI argument parsing을 확인한다.
- `internal/greeting` 패키지에 이름을 받는 함수 또는 기존 함수를 확장한다.
- 단위 테스트를 추가하거나 갱신한다.
- `go test ./...`가 통과해야 한다.
