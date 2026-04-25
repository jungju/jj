# jj

`jj`는 기획 파일을 실행 가능한 `docs/SPEC.md`와 `docs/TASK.md`로 바꾸고, Codex 구현/테스트와 결과 평가까지 한 번에 연결하는 Go 기반 orchestration CLI입니다.

아이디어를 적은 `plan.md` 하나만 있으면 `jj`가 병렬 기획 에이전트로 구현 명세를 만들고, Codex에게 실제 작업을 맡기며, 모든 결과를 `.jj/runs/<run-id>/`에 남깁니다. `OPENAI_API_KEY`가 있으면 OpenAI API로 기획/평가를 수행하고, 없으면 Codex CLI로 fallback합니다.

## 무엇을 해주나요?

`jj run <plan.md>`는 다음 흐름을 자동화합니다.

1. 사용자의 기획 파일을 읽습니다.
2. 대상 git workspace 상태를 기록합니다.
3. 여러 기획 에이전트가 `SPEC.md`, `TASK.md` 초안을 만듭니다.
4. 초안을 병합해 최종 `docs/SPEC.md`, `docs/TASK.md`를 생성합니다.
5. non-dry-run에서는 Codex CLI가 구현과 테스트를 수행합니다.
6. git diff/status와 Codex 결과를 평가해 `docs/EVAL.md`를 만듭니다.
7. 모든 입력, 출력, 이벤트, manifest를 `.jj/runs/<run-id>/`에 저장합니다.

`jj serve`의 첫 화면은 대시보드입니다. 현재 `docs/TASK.md` 상태, 진행 중인 run, 최근 실행 결과, 평가 상태를 먼저 보여주고, 생성된 README, TASK/SPEC/EVAL, manifest, planning artifact로 이동할 수 있게 합니다.

`jj`의 개발 원칙은 문서 기반입니다. 기능 변경은 `plan.md`, `docs/SPEC.md`, `docs/TASK.md`, README 같은 문서에서 시작하고, 구현 후에도 문서와 실제 동작이 일치해야 합니다.

## 빠른 시작

빌드합니다.

```bash
go build -o jj ./cmd/jj
```

제품 기획서 샘플인 루트 `plan.md`로 dry-run을 실행합니다.

```bash
./jj run plan.md --dry-run
```

대시보드에서 현재 TASK 상태와 실행 아티팩트를 확인합니다.

```bash
./jj serve --cwd .
```

터미널에 출력된 `http://127.0.0.1:7331` 주소를 브라우저에서 엽니다. 브라우저는 자동으로 열리지 않습니다.

## 요구 사항

- Go 1.25 이상
- `PATH`에서 실행 가능한 Codex CLI 또는 `JJ_CODEX_BIN`
- OpenAI API로 기획/평가를 수행하려면 `OPENAI_API_KEY`
- 대상 workspace는 기본적으로 git 저장소여야 합니다.

`OPENAI_API_KEY`가 없으면 `jj`는 Codex CLI로 기획/병합/평가를 수행합니다. 단, key가 잘못되었거나 만료된 경우에는 OpenAI 호출 실패로 처리하고 자동 fallback하지 않습니다.

## 기본 사용법

현재 디렉터리를 대상으로 실행합니다.

```bash
./jj run plan.md
```

다른 workspace를 대상으로 실행합니다.

```bash
./jj run plan.md --cwd /path/to/repo
```

기획 산출물만 확인합니다.

```bash
./jj run plan.md --dry-run
```

dry-run은 plan 읽기, git 확인, planning, merge, run directory 안의 `docs/SPEC.md`/`docs/TASK.md`와 manifest 생성을 수행합니다. 대상 워크트리에는 `docs/SPEC.md`/`docs/TASK.md`를 쓰지 않고, 구현 Codex와 post-Codex 평가는 실행하지 않습니다. 다만 `OPENAI_API_KEY`가 없으면 기획/병합을 위해 Codex CLI가 실행될 수 있습니다.

## 명령

### `jj run <plan.md>`

기획 파일을 바탕으로 SPEC/TASK 생성, Codex 구현, 결과 평가를 수행합니다.

```text
--cwd DIR              대상 저장소 디렉터리
--run-id ID            .jj/runs/<run-id>에 사용할 실행 ID
--agents N             병렬 기획 에이전트 수, 기본값 3
--planning-agents N    --agents의 이전 이름
--openai-model MODEL   기획 및 평가에 사용할 OpenAI 모델
--codex-model MODEL    Codex CLI에 넘길 모델
--spec-doc NAME        docs/ 아래에 쓸 SPEC 문서명, 기본값 SPEC.md
--task-doc NAME        docs/ 아래에 쓸 TASK 문서명, 기본값 TASK.md
--eval-doc NAME        docs/ 아래에 쓸 EVAL 문서명, 기본값 EVAL.md
--allow-no-git         git 저장소가 아닌 곳에서도 실행
--dry-run              기획 산출물만 생성
```

문서명 옵션은 파일명만 받습니다. 예를 들어 `--spec-doc PRODUCT_SPEC.md`는 `docs/PRODUCT_SPEC.md`를 생성합니다. `docs/SPEC.md`, `../SPEC.md`, `/tmp/SPEC.md`처럼 경로가 포함된 값은 거부됩니다.

기본 planning agent:

- `product_spec`
- `implementation_tasking`
- `qa_evaluation`

기본 3개 중 일부가 실패해도 최소 1개 이상 성공하면 merge를 시도합니다. 0개가 성공하면 실행은 실패합니다.

### `jj serve`

대시보드를 첫 화면으로 띄우고 README와 `.jj/runs` 아티팩트를 로컬 웹페이지로 보여줍니다.

```text
--cwd DIR       문서와 .jj/runs를 읽을 디렉터리
--addr ADDR     서버 listen 주소, 기본값 127.0.0.1:7331
--run-id ID     기본으로 강조할 run id
```

예:

```bash
./jj serve --cwd .
./jj serve --cwd playground/workspace
```

대시보드는 현재 `docs/TASK.md` 요약, 진행 중인 run, 최근 run status, 평가 결과, 실패/위험 항목, 다음 액션을 먼저 보여주는 화면입니다. 문서 목록과 artifact 상세 화면은 대시보드에서 들어가는 보조 화면입니다.

## 설정

환경 변수:

```text
OPENAI_API_KEY       있으면 OpenAI 기획 및 평가 API 호출에 사용
JJ_OPENAI_MODEL      기본 OpenAI 모델 override
JJ_CODEX_BIN         Codex 바이너리 경로 override
JJ_CODEX_MODEL       Codex 모델 override
```

`.jjrc` 설정 파일도 사용할 수 있습니다. `jj`는 명령을 실행한 현재 디렉터리에서 시작해 상위 디렉터리로 올라가며 첫 번째 `.jjrc`를 자동으로 읽습니다. `--cwd`는 대상 workspace만 지정하며 `.jjrc` 탐색 시작점은 바꾸지 않습니다.

```json
{
  "openai_api_key_env": "OPENAI_API_KEY",
  "openai_model": "gpt-5.5",
  "codex_model": "gpt-5.5",
  "codex_bin": "codex",
  "planning_agents": 3,
  "spec_doc": "SPEC.md",
  "task_doc": "TASK.md",
  "eval_doc": "EVAL.md",
  "dry_run": false,
  "allow_no_git": false
}
```

`.jjrc`에는 실제 API key를 저장하지 않습니다. `openai_api_key_env`에는 API key가 들어 있는 환경 변수 이름만 적습니다.

설정 우선순위:

1. CLI flag
2. 환경 변수
3. `.jjrc`
4. 기본값

## 아티팩트

각 실행은 아래 디렉터리에 저장됩니다.

```text
.jj/runs/<run-id>/
```

주요 파일:

```text
input.md
planning/product_spec.json
planning/implementation_tasking.json
planning/qa_evaluation.json
planning/merge.json
planning/eval.json
docs/SPEC.md
docs/TASK.md
codex-events.jsonl
codex-summary.md
git-diff.patch
git-diff-summary.txt
docs/EVAL.md
git-baseline.json
git-status.txt
manifest.json
```

Codex fallback으로 기획/병합/평가를 수행한 경우 다음 파일도 생성될 수 있습니다.

```text
planning/<agent>.events.jsonl
planning/<agent>.last-message.txt
planning/merge.events.jsonl
planning/merge.last-message.txt
planning/eval.events.jsonl
planning/eval.last-message.txt
```

dry-run에서도 `.jj/runs/<run-id>/docs/SPEC.md`와 `.jj/runs/<run-id>/docs/TASK.md`는 생성됩니다. non-dry-run에서는 대상 워크트리에도 `docs/SPEC.md`와 `docs/TASK.md`가 생성됩니다.

run-id를 지정하지 않으면 다음 형식으로 생성됩니다.

```text
YYYYMMDD-HHMMSS-<short-random>
```

run-id는 `a-z A-Z 0-9 . _ -`만 허용합니다. 이미 같은 run directory가 있으면 덮어쓰지 않고 실패합니다.

## manifest.json

`manifest.json`은 실행의 단일 진실 공급원입니다.

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
    "config_file": "/path/to/.jjrc",
    "openai_api_key_env": "OPENAI_API_KEY",
    "openai_api_key_present": false,
    "allow_no_git": false,
    "spec_doc": "SPEC.md",
    "task_doc": "TASK.md",
    "eval_doc": "EVAL.md"
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

## Playground

repo 루트를 오염시키지 않고 시험하려면 `playground/`를 사용합니다.

```bash
./playground/setup.sh
./jj run playground/plan.md --cwd playground/workspace --dry-run
./jj serve --cwd playground/workspace
```

루트 `plan.md`는 jj 자체의 제품 기획서입니다. `playground/plan.md`는 샘플 Go workspace를 수정해보는 작은 실행 예제입니다.

## 보안

- `OPENAI_API_KEY`, bearer token, password, secret 값은 manifest나 에러 아티팩트에 기록하지 않습니다.
- `.jjrc`에는 API key 값을 직접 저장하지 말고 `openai_api_key_env`만 선언합니다.
- 환경 변수 전체를 로그로 출력하지 않습니다.
- OpenAI/Codex 원문 응답을 저장할 때도 실패 원문은 필요한 일부만 저장하고 secret 후보 값은 redaction합니다.
- `jj serve`는 path traversal을 차단하고 화면에 표시하기 전에 secret 후보 문자열을 redaction합니다.

## 개발 원칙

- 모든 개발은 문서를 기반으로 진행합니다.
- 기능을 바꾸기 전에는 관련 `plan.md`, `docs/SPEC.md`, `docs/TASK.md`, README를 먼저 갱신하거나 생성합니다.
- 구현이 끝난 뒤에는 문서가 실제 CLI 동작, artifact 구조, 웹 대시보드 상태와 일치하는지 확인합니다.
- `jj serve`의 첫 화면은 항상 현재 작업 상태를 보는 대시보드여야 하며, 단순 파일 목록이 첫 경험이 되면 안 됩니다.

## 개발

```bash
go test ./...
go vet ./...
go build -o jj ./cmd/jj
```

테스트 구조:

- OpenAI/Codex planner/evaluator는 `PlanningClient` 인터페이스로 분리되어 fake 구현으로 대체할 수 있습니다.
- Codex 실행은 `CodexRunner` 인터페이스로 분리되어 fake runner로 테스트할 수 있습니다.
- git 실행은 `GitRunner` 인터페이스로 분리되어 있습니다.
- artifact는 temp directory 기반 테스트와 atomic write를 사용합니다.
