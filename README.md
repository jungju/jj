# jj

`jj`는 Go 기반 CLI로, 사용자가 작성한 기획 파일을 읽어 `SPEC.md`와 `TASK.md`를 만들고 Codex에 구현과 테스트를 맡긴 뒤 결과를 평가합니다. `OPENAI_API_KEY`가 있으면 OpenAI API로 기획/평가를 수행하고, 없으면 Codex CLI로 fallback합니다.

현재 공개 CLI 표면은 의도적으로 작게 유지합니다.

```bash
jj run <plan.md>
```

## 요구 사항

- Go 1.25 이상
- `PATH`에서 실행 가능한 Codex CLI 또는 `JJ_CODEX_BIN`
- OpenAI API로 기획/평가를 하려면 `OPENAI_API_KEY`
- 대상 워크스페이스는 기본적으로 git 저장소여야 합니다.
  - git 저장소가 아닌 곳에서 실행하려면 `--allow-no-git`를 사용합니다.

## 설치

```bash
go build -o jj ./cmd/jj
```

소스에서 바로 도움말을 확인할 수도 있습니다.

```bash
go run ./cmd/jj --help
go run ./cmd/jj run --help
```

## 사용법

기획 파일을 준비합니다.

```markdown
# Plan

작은 CLI 기능을 만든다...
```

현재 저장소에서 전체 파이프라인을 실행합니다.

```bash
jj run plan.md
```

`OPENAI_API_KEY`가 설정되어 있으면 OpenAI API를 사용하고, 없으면 Codex CLI가 기획/병합/평가도 수행합니다.

다른 워크스페이스를 대상으로 실행합니다.

```bash
jj run plan.md --cwd /path/to/repo
```

기획 산출물만 확인하려면 dry-run을 사용합니다.

```bash
jj run plan.md --dry-run
```

dry-run은 plan 읽기, git 확인, planning, merge, run directory 안의 `SPEC.md`/`TASK.md`와 manifest 생성을 수행합니다. 워크트리 루트에 `SPEC.md`/`TASK.md`를 쓰지 않고, 구현 Codex 실행과 post-Codex 평가는 수행하지 않습니다. 단, `OPENAI_API_KEY`가 없으면 기획/병합을 위해 Codex CLI가 실행될 수 있습니다.

## 로컬 playground에서 CLI 테스트하기

repo 루트를 오염시키지 않고 `jj run`을 시험하려면 `playground/`를 사용합니다.

```bash
go build -o jj ./cmd/jj
./playground/setup.sh
./jj run playground/plan.md --cwd playground/workspace --dry-run
```

full-run을 시험하려면:

```bash
./jj run playground/plan.md --cwd playground/workspace
```

실행 산출물은 `playground/workspace/.jj/runs/<run-id>/` 아래에 생성됩니다. 자세한 사용법과 정리 방법은 `playground/README.md`를 참고하세요.
`playground/plan.md`는 명령을 실행한 현재 셸 위치 기준으로 해석되고, `--cwd`는 수정 대상 workspace만 지정합니다.

## 옵션

```text
--cwd DIR              대상 저장소 디렉터리
--run-id ID            .jj/runs/<run-id>에 사용할 실행 ID
--planning-agents N    병렬 기획 에이전트 수, 기본값 3
--openai-model MODEL   기획 및 평가에 사용할 OpenAI 모델
--codex-model MODEL    Codex CLI에 넘길 모델
--allow-no-git         git 저장소가 아닌 곳에서도 실행
--dry-run              기획 산출물만 생성
```

## 환경 변수

```text
OPENAI_API_KEY       있으면 OpenAI 기획 및 평가 API 호출에 사용
JJ_OPENAI_MODEL      기본 OpenAI 모델 override
JJ_CODEX_BIN         Codex 바이너리 경로 override
JJ_CODEX_MODEL       Codex 모델 override
```

.jjrc 설정 파일도 사용할 수 있습니다. `jj`는 명령을 실행한 현재 디렉터리에서 시작해 상위 디렉터리로 올라가며 첫 번째 `.jjrc`를 자동으로 읽습니다. `--cwd`는 대상 workspace만 지정하며 `.jjrc` 탐색 시작점은 바꾸지 않습니다.

```json
{
  "openai_api_key_env": "OPENAI_API_KEY",
  "openai_model": "gpt-5.5",
  "codex_model": "gpt-5.5",
  "codex_bin": "codex",
  "planning_agents": 3
}
```

`.jjrc`에는 실제 API key를 저장하지 않습니다. `openai_api_key_env`에는 API key가 들어 있는 환경 변수 이름만 적습니다.

설정 우선순위:

1. CLI flag
2. 환경 변수
3. `.jjrc`
4. 기본값

OpenAI 모델 결정 순서:

1. `--openai-model`
2. `JJ_OPENAI_MODEL`
3. `.jjrc`의 `openai_model`
4. `gpt-5.5`

Codex 모델 결정 순서:

1. `--codex-model`
2. `JJ_CODEX_MODEL`
3. `.jjrc`의 `codex_model`
4. Codex CLI 기본값

## 파이프라인

1. `<plan.md>`를 읽습니다.
2. 대상 디렉터리의 git 상태를 확인합니다.
3. `.jj/runs/<run-id>/` 디렉터리를 만듭니다.
4. 기획 에이전트들을 병렬 실행합니다.
   - `OPENAI_API_KEY`가 있으면 OpenAI Responses API를 사용합니다.
   - `OPENAI_API_KEY`가 없으면 Codex CLI를 사용합니다.
   - `product_spec`
   - `implementation_tasking`
   - `qa_evaluation`
5. 성공한 에이전트 결과를 병합해 최종 `SPEC.md`와 `TASK.md`를 만듭니다.
6. non-dry-run 모드에서는 `SPEC.md`와 `TASK.md`를 대상 워크트리 루트에 씁니다.
7. `codex exec`를 실행하고 `codex-events.jsonl`, `codex-summary.md`를 기록합니다.
8. 실행 후 git diff/status를 캡처합니다.
9. 평가를 실행해 `EVAL.md`와 `planning/eval.json`을 만듭니다.
10. 전체 실행 정보를 `manifest.json`에 기록합니다.

기본 planning agent 중 일부가 실패해도 최소 1개 이상 성공하면 merge를 시도합니다. 0개가 성공하면 실행은 실패합니다.

## 아티팩트

각 실행은 아래 디렉터리에 저장됩니다.

```text
.jj/runs/<run-id>/
```

생성 파일:

```text
input.md
planning/product_spec.json
planning/implementation_tasking.json
planning/qa_evaluation.json
planning/merge.json
planning/eval.json
SPEC.md
TASK.md
codex-events.jsonl
codex-summary.md
git-diff.patch
git-diff-summary.txt
EVAL.md
manifest.json
```

Codex fallback으로 기획/병합/평가를 수행한 경우 다음 이벤트/최종 메시지 파일도 함께 생성될 수 있습니다.

```text
planning/<agent>.events.jsonl
planning/<agent>.last-message.txt
planning/merge.events.jsonl
planning/merge.last-message.txt
planning/eval.events.jsonl
planning/eval.last-message.txt
```

dry-run에서도 `.jj/runs/<run-id>/SPEC.md`와 `.jj/runs/<run-id>/TASK.md`는 생성됩니다. non-dry-run에서는 대상 워크트리 루트에도 `SPEC.md`와 `TASK.md`가 생성됩니다.

`--run-id`를 지정하지 않으면 다음 형식으로 생성됩니다.

```text
YYYYMMDD-HHMMSS-<short-random>
```

예:

```text
20260425-152233-a1b2c3
```

run-id는 `a-z A-Z 0-9 . _ -`만 허용합니다. 이미 같은 run directory가 있으면 덮어쓰지 않고 실패합니다.

## manifest.json

`manifest.json`은 실행의 단일 진실 공급원입니다.

주요 필드:

```json
{
  "schema_version": "1",
  "run_id": "...",
  "status": "success|partial|failed|cancelled",
  "dry_run": false,
  "planner_provider": "openai|codex|injected",
  "git": {
    "is_repo": true,
    "root": "...",
    "branch": "...",
    "head": "...",
    "initial_status": "...",
    "final_status": "...",
    "diff_path": "git-diff.patch"
  },
  "config": {
    "planning_agents": 3,
    "openai_model": "gpt-5.5",
    "codex_model": "...",
    "codex_bin": "...",
    "config_file": "/path/to/.jjrc"
  },
  "planning": {
    "agents": []
  },
  "codex": {
    "ran": true,
    "exit_code": 0,
    "duration_ms": 0
  },
  "evaluation": {
    "ran": true,
    "result": "PASS|PARTIAL|FAIL",
    "score": 0
  },
  "errors": []
}
```

## 보안

- `OPENAI_API_KEY`, bearer token, password, secret 값은 manifest나 에러 아티팩트에 기록하지 않습니다.
- `.jjrc`에는 API key 값을 직접 저장하지 말고 `openai_api_key_env`만 선언합니다.
- 환경 변수 전체를 로그로 출력하지 않습니다.
- OpenAI/Codex 원문 응답을 저장할 때도 실패 원문은 필요한 일부만 저장하고 secret 후보 값은 redaction합니다.

## 개발

테스트:

```bash
go test ./...
```

정적 검사:

```bash
go vet ./...
```

빌드:

```bash
go build -o jj ./cmd/jj
```

## 테스트 구조

- OpenAI/Codex planner/evaluator는 `PlanningClient` 인터페이스로 분리되어 fake 구현으로 대체할 수 있습니다.
- Codex 실행은 `CodexRunner` 인터페이스로 분리되어 fake runner로 테스트할 수 있습니다.
- git 실행은 `GitRunner` 인터페이스로 분리되어 있습니다.
- artifact는 temp directory 기반 테스트와 atomic write를 사용합니다.
