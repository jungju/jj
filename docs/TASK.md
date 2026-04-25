# TASK

## Objective

`jj`의 다음 단계 기능을 구현 가능한 로드맵으로 정리한다. 중심 목표는 로컬 우선 AI coding orchestration CLI라는 제품 의도를 유지하면서, 브라우저에서 실행을 시작하고, 실행 전후 hook을 연결하고, run 상태와 결과를 더 잘 관찰할 수 있게 만드는 것이다.

이 문서는 구현 지시서다. 각 기능은 별도 PR 또는 별도 `jj run` 계획으로 분리해도 되지만, 공개 CLI와 artifact 신뢰성을 깨뜨리지 않아야 한다.

## Constraints

- 기본 제품 방향은 local-first다. `jj`는 cloud service나 multi-user dashboard가 아니다.
- 새 기능은 기존 `jj run <plan.md>`와 `jj serve` 흐름을 보강해야 하며, 기존 사용법을 깨뜨리지 않는다.
- 모든 실행 결과는 `.jj/runs/<run-id>/`와 `manifest.json`에 감사 가능한 형태로 남긴다.
- secret 값은 로그, manifest, artifact, 웹 화면에 노출하지 않는다.
- destructive action, full-run, hook 실행은 사용자가 명시적으로 확인할 수 있어야 한다.
- OpenAI API와 Codex CLI를 실제 호출하지 않아도 테스트 가능한 인터페이스를 유지한다.
- `jj serve`의 기본 listen 주소는 local-only인 `127.0.0.1` 계열을 유지한다.

## Feature 1: Web Run

`jj serve` 웹 UI에서 `jj run`을 시작할 수 있게 한다.

구현 요구사항:

1. `jj serve` 홈 또는 별도 `/run/new` 화면에 run form을 추가한다.
2. 입력 필드는 최소한 `plan path`, `cwd`, `dry-run`, `planning agents`, `OpenAI model`, `Codex model`, `run-id`를 포함한다.
3. `dry-run`은 기본값으로 켜는 것을 우선 검토한다.
4. full-run을 선택하면 코드 변경과 hook 실행 가능성을 알리는 명시적 확인 UI를 제공한다.
5. form submit은 서버 내부에서 기존 run pipeline을 호출해야 하며, CLI path를 shell로 조합해 실행하지 않는다.
6. 실행 시작 직후 run id와 run detail URL을 보여준다.
7. 동시에 여러 run을 시작할 수 있는 경우 run별 상태가 섞이지 않도록 run id 단위로 격리한다.
8. 실행 실패 시 브라우저에는 짧고 명확한 에러를 보여주고, 상세 원인은 artifact와 manifest에 기록한다.

권장 엔드포인트:

- `GET /run/new`: run 생성 form
- `POST /run`: run 시작
- `GET /run?id=<run-id>`: 기존 run detail 화면 재사용

수용 기준:

- 브라우저에서 dry-run을 시작하면 `.jj/runs/<run-id>/docs/SPEC.md`, `docs/TASK.md`, `manifest.json`이 생성된다.
- full-run은 확인 UI 없이는 시작되지 않는다.
- path traversal 입력은 거부된다.
- API key나 bearer token은 response body에 그대로 노출되지 않는다.

## Feature 2: Hooks

사용자가 run 단계 전후에 로컬 명령을 연결할 수 있게 한다.

지원 단계:

- `pre_plan`
- `post_plan`
- `pre_codex`
- `post_codex`
- `post_eval`
- `on_failure`

설정 방향:

`.jjrc`를 확장해 hook을 선언한다. 실제 명령에는 secret 값을 직접 넣지 않도록 문서화하고, manifest에는 hook command 전체를 저장하지 않거나 redaction된 형태로만 저장한다.

예시:

```json
{
  "hooks": {
    "pre_plan": [
      {
        "name": "check workspace",
        "command": "git status --short",
        "required": true
      }
    ],
    "post_eval": [
      {
        "name": "print eval",
        "command": "sed -n '1,120p' \"$JJ_RUN_DIR/docs/EVAL.md\"",
        "required": false
      }
    ]
  }
}
```

실행 환경:

- `JJ_RUN_ID`
- `JJ_RUN_DIR`
- `JJ_CWD`
- `JJ_STATUS`
- `JJ_DRY_RUN`
- `JJ_PLANNER_PROVIDER`

구현 요구사항:

1. hook 설정 파서를 추가하고 unknown field는 명확한 에러로 처리한다.
2. hook 실행 결과를 `.jj/runs/<run-id>/hooks/<stage>/<name>.log`에 저장한다.
3. `required: true` hook 실패는 pipeline 실패로 처리한다.
4. `required: false` hook 실패는 manifest 경고로 남기고 가능한 다음 단계로 진행한다.
5. hook timeout 기본값을 둔다. 권장 기본값은 5분이다.
6. SIGINT/SIGTERM 시 실행 중 hook 프로세스를 종료하고 manifest에 `cancelled`를 기록한다.

수용 기준:

- 각 단계 hook이 올바른 순서로 실행된다.
- hook stdout/stderr가 artifact에 기록된다.
- required hook 실패 시 Codex 구현 단계가 실행되지 않는다.
- secret 후보 문자열은 hook log 표시 화면에서 redaction된다.

## Feature 3: Monitoring

run 진행 상태와 결과를 웹에서 추적할 수 있게 한다.

구현 요구사항:

1. `manifest.json`에 단계별 status를 기록할 수 있는 구조를 추가한다.
2. 단계는 최소 `read_plan`, `git_baseline`, `planning`, `merge`, `write_outputs`, `codex`, `git_capture`, `evaluation`, `hooks`를 포함한다.
3. `jj serve` run detail 화면에 timeline을 표시한다.
4. planner events, Codex events, hook logs, git diff summary, EVAL을 한 화면에서 이동할 수 있게 링크를 정리한다.
5. 가능하면 Server-Sent Events로 live update를 제공한다.
6. SSE가 어렵거나 브라우저 호환성이 문제가 되면 interval polling으로 시작한다.
7. 실행 중 run과 완료된 run을 목록에서 구분한다.

권장 엔드포인트:

- `GET /runs`: run 목록 JSON
- `GET /run/status?id=<run-id>`: 단일 run status JSON
- `GET /run/events?id=<run-id>`: SSE 또는 polling용 event stream

수용 기준:

- 실행 중인 run의 현재 단계가 웹에서 확인된다.
- Codex non-zero exit도 timeline과 manifest에 표시된다.
- 완료된 run은 `PASS`, `PARTIAL`, `FAIL`, `success`, `failed`, `cancelled` 상태를 빠르게 확인할 수 있다.
- monitoring UI는 secret 값을 표시하지 않는다.

## Feature 4: Run Review and Comparison

여러 run의 결과를 비교해 어떤 시도가 더 나은지 판단할 수 있게 한다.

구현 요구사항:

1. run 목록에 run id, started_at, status, planner provider, evaluation result, score, changed files 수를 표시한다.
2. 두 run을 선택해 `docs/SPEC.md`, `docs/TASK.md`, `docs/EVAL.md`, `git-diff-summary.txt`를 나란히 비교할 수 있게 한다.
3. 비교 기능은 파일 원문을 안전하게 escape해서 보여준다.
4. run detail 화면에서 이전/다음 run으로 이동할 수 있게 한다.
5. `PASS/PARTIAL/FAIL`, score, test result, Codex exit code를 스캔하기 쉽게 표시한다.

권장 엔드포인트:

- `GET /compare?left=<run-id>&right=<run-id>`

수용 기준:

- 사용자는 두 run의 SPEC/TASK/EVAL 차이를 브라우저에서 확인할 수 있다.
- 존재하지 않는 run id는 404로 처리한다.
- 비교 대상 파일이 없으면 화면 전체가 실패하지 않고 해당 파일만 missing으로 표시한다.

## Feature 5: Configuration and Safety

웹 실행, hook, monitoring을 안전하게 운영하기 위한 설정과 보호 장치를 추가한다.

구현 요구사항:

1. `.jjrc`에 web run과 hooks 관련 설정을 추가한다.
2. 기본값은 보수적으로 둔다.
3. `jj serve`가 `127.0.0.1`이 아닌 주소로 bind될 때 web run 기능을 기본 비활성화하거나 경고를 명확히 표시한다.
4. POST 요청에는 최소한 same-origin 확인 또는 local-only 전제를 검증하는 보호 장치를 둔다.
5. hook command와 web run 입력값은 shell injection 위험을 고려해 검증한다.
6. manifest에는 설정값 중 secret이 아닌 것만 기록한다.
7. README에는 web run과 hooks가 로컬 명령 실행 기능임을 분명히 문서화한다.

권장 `.jjrc` 예시:

```json
{
  "serve": {
    "web_run_enabled": true,
    "allow_full_run_from_web": false
  },
  "hooks": {}
}
```

수용 기준:

- 기본 설정에서 우발적인 원격 full-run이 불가능하다.
- `.jjrc`에 잘못된 hook 설정이 있으면 명확한 에러를 낸다.
- manifest와 웹 화면에 API key 값이 남지 않는다.

## Testing Requirements

문서 변경 후 실제 구현 단계에서는 다음 테스트를 추가한다.

- Web run handler test: form render, dry-run submit, full-run confirmation required.
- Web run pipeline test: fake planner와 fake Codex runner로 browser-triggered run이 artifact를 생성하는지 확인.
- Local-only safety test: non-local bind에서 web run 제한 또는 경고가 동작하는지 확인.
- Hook config test: valid config parsing, unknown field failure, invalid stage failure.
- Hook execution test: required hook failure stops pipeline, optional hook failure records warning.
- Hook artifact test: stdout/stderr log와 manifest hook result가 생성되는지 확인.
- Monitoring test: run status JSON과 run detail timeline이 manifest 상태를 반영하는지 확인.
- SSE or polling test: 실행 중 status update가 관찰 가능한지 확인.
- Comparison test: 두 run의 SPEC/TASK/EVAL/diff summary가 안전하게 표시되는지 확인.
- Secret redaction test: API key, bearer token, password 후보가 HTML response와 manifest에 그대로 노출되지 않는지 확인.

품질 게이트:

```bash
go test ./...
go vet ./...
go build -o jj ./cmd/jj
```

## Done Criteria

- `docs/TASK.md`에 다음 기능 로드맵이 정리되어 있다.
  - 웹에서 Run하기
  - Hook 기능
  - 모니터링 기능
  - Run review and comparison
  - Configuration and safety
- 문서는 기본적으로 `docs/` 아래에 둔다.
- 각 기능은 구현 요구사항과 수용 기준을 포함한다.
- 후속 구현자가 `docs/TASK.md`만 읽고 작업 범위와 테스트 방향을 이해할 수 있다.
