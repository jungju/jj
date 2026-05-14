# jj 제품 기획서

## 문제 정의

AI 코딩 워크플로우에서는 기획, 구현, 테스트, 평가가 서로 다른 프롬프트와 파일, 터미널 세션에 흩어지기 쉽습니다. 이러면 같은 맥락을 반복해서 설명해야 하고, 산출물을 비교하기 어렵고, 최종 코드가 원래 의도를 만족했는지도 확인하기 어렵습니다.

`jj`는 이 반복 비용을 줄이기 위한 local-first CLI입니다. 사용자가 한 번 작성한 `docs/PLAN.md`를 시작점으로 삼고, 이후에는 `data/documents.sqlite3`에 현재 workspace의 실행 가능한 SPEC/task 상태를 유지하면서 Codex 실행, deterministic validation, 증거 기록까지 하나의 재현 가능한 흐름으로 묶습니다.

## 문서와 상태의 역할

- `README.md`, `docs/PLAN.md`, `docs/PRD.md`, `docs/SPEC.md`, `docs/TASK.md`는 사람이 읽는 제품 경계와 개발 문서다.
- `data/documents.sqlite3`는 bootstrap 이후 planning source of truth인 현재 SPEC과 run마다 append되는 workspace task proposal history를 저장한다.
- `.jj/spec.json`과 `.jj/tasks.json`은 legacy import path이자 SQLite-backed virtual dashboard JSON view다.
- `.jj/runs/<run-id>/`는 manifest, snapshots, logs, validation, summaries를 담는 local run evidence다.
- 같은 SQLite DB는 redacted `.jj/` 문서를 모아 두는 local document history/search 저장소로도 쓰인다.
- `docs/` Markdown은 current product boundary 문서지만, current runs가 자동으로 갱신하는 runtime state가 아니다.

## 목표

- 하나의 기획 seed에서 canonical SQLite SPEC과 append-only task history를 만들고 유지한다.
- 제품 요구사항, 구현 단계, 검증 기준, 보안 리스크가 모두 반영되도록 planning context를 구성한다.
- 현재 workspace에서 Codex가 구현과 테스트를 수행하도록 연결한다.
- 모든 실행 결과를 `.jj/runs/<run-id>/` 아래에 남겨 검토와 비교가 가능하게 한다.
- `OPENAI_API_KEY`가 있으면 OpenAI API를 사용하고, 없으면 Codex CLI fallback으로 동작한다.
- `jj serve`로 product docs, SQLite state, run artifacts를 guarded local dashboard에서 볼 수 있게 한다.
- 모든 개발이 written intent에서 시작하고 SQLite state, validation, git diff, run evidence로 검토되도록 한다.
- 웹 UI는 첫 화면부터 현재 SPEC/task 상태와 진행 상황을 보여주는 dashboard-first 경험으로 만든다.

## 사용자

- Codex를 사용하면서 기획부터 구현까지 반복 가능한 흐름을 만들고 싶은 개인 개발자.
- AI가 만든 변경에도 명세, 작업 지시, diff, 평가 증거를 남기고 싶은 소규모 팀.
- AI coding workflow를 실험하고 각 실행 결과를 감사 가능한 형태로 보관하려는 사용자.

## 핵심 워크플로우

1. 사용자가 원하는 제품 또는 코드 변경을 `docs/PLAN.md`에 작성한다.
2. 사용자가 `jj run docs/PLAN.md --dry-run`으로 planning preview와 snapshots를 먼저 확인할 수 있다.
3. 사용자가 `jj run docs/PLAN.md`를 실행한다.
4. `jj`가 입력 파일, current SQLite SPEC, SQLite task 요약, 최근 run evidence, git baseline을 기록한다.
5. Planner가 fresh task batch를 만들고 SQLite task state에 append한다.
6. Full run은 새 runnable task 하나를 선택하고 Codex CLI를 기본 구현 provider로 실행한다.
7. `jj`가 Codex events, summary, git status, git diff, validation result를 `.jj/runs/<run-id>/`에 캡처한다.
8. validation이 통과하면 선택 task를 `done`으로 표시하고 결과 기반으로 SQLite SPEC state를 reconcile한다.
9. validation이 실패, 누락, skipped이면 기존 SQLite SPEC state를 유지한다.
10. 사용자가 `jj serve --cwd .`의 대시보드에서 product docs, SQLite state, task 상태, run evidence, validation 상태를 검토한다.

## 기능 요구사항

- `jj run <plan-file.md>`는 비어 있지 않은 Markdown 기획 파일을 읽는다.
- `--cwd`는 대상 workspace를 지정하되, 상대 plan 경로 해석 기준을 바꾸지 않는다.
- 기본적으로 git 저장소에서 실행하며, 예외적으로 `--allow-no-git`를 지원한다.
- `--dry-run`은 planning artifact와 state snapshot만 `.jj/runs/<run-id>/`에 만들고 workspace SQLite state 쓰기와 구현 Codex 실행을 하지 않는다.
- full run은 planning 중 SQLite task state에 새 task를 append하고, validation 성공 후에만 SQLite SPEC state를 갱신한다.
- planner provider 선택은 다음 순서를 따른다.
  - 테스트와 내부 주입용 injected planner
  - API key가 있을 때 OpenAI planner
  - API key가 없을 때 Codex CLI fallback planner
- 생성 아티팩트에는 input snapshot, planning JSON, SPEC/TASK snapshots, Codex events/summary, git evidence, validation evidence, `manifest.json`이 포함된다.
- `jj serve`는 첫 화면을 대시보드로 제공하고, allowlisted product docs, SQLite-backed state views, run artifact를 로컬 HTTP 서버로 탐색하게 한다.
- 대시보드는 현재 SQLite task 상태, 진행 중인 run, 최근 run status, validation 결과, 실패/위험 항목, 다음 액션을 보여준다.
- 구현 변경은 관련 public docs 또는 SQLite state와 실제 동작이 일치해야 완료로 본다.

## 설정 요구사항

- cwd, run id, planning agent 수, OpenAI model, Codex model, no-git mode, dry-run을 CLI flag로 설정할 수 있다.
- OpenAI key/model과 Codex binary/model을 환경 변수로 설정할 수 있다.
- `.jjrc`로 프로젝트 기본값을 선언할 수 있다.
- `.jjrc`, manifest, log, 웹 화면에 실제 API key나 bearer token을 기록하지 않는다.

## 성공 기준

- 사용자가 `jj run docs/PLAN.md --dry-run`을 실행하고 `.jj/runs` 아래의 planning artifacts와 state snapshots를 확인할 수 있다.
- `OPENAI_API_KEY`가 없어도 Codex CLI를 통해 planning을 실행할 수 있다.
- non-dry-run은 구현 증거, git diff summary, validation artifacts를 생성한다.
- `manifest.json`은 run status, config, git metadata, planner provider, Codex result, validation result를 한눈에 확인할 수 있게 한다.
- `jj serve --cwd .`는 대시보드에서 SPEC/task 상태, 진행 상황, 최근 validation 결과를 secret 노출 없이 보여준다.
- 사용자는 대시보드를 시작점으로 README, `docs/PLAN.md`, project docs, SQLite-backed `.jj/spec.json`/`.jj/tasks.json` views, `.jj/runs` 아티팩트를 검토할 수 있다.
- 변경된 기능은 문서, SQLite state, 구현이 함께 맞아떨어지도록 검토되어 document-first 개발 흐름을 유지한다.
- `go test ./...`, `go vet ./...`, `go build -o jj ./cmd/jj`가 통과한다.

## 비범위

- `jj`는 cloud service가 아니다.
- `jj`는 multi-user dashboard가 아니다.
- `jj`는 임의 DAG를 실행하는 범용 workflow engine이 아니다.
- `jj`는 git review를 대체하지 않는다. 사람이 더 쉽게 검토할 수 있도록 증거를 남기는 도구다.
- `jj`는 AI 출력의 정답성을 보장하지 않는다. 대신 과정을 감사 가능하게 만든다.
