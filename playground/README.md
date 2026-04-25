# jj playground

이 폴더는 `jj run`을 repo 루트가 아니라 하위 workspace에서 안전하게 시험하기 위한 공간입니다.

실제 OpenAI/Codex 호출은 아래 명령을 직접 실행할 때만 발생합니다. `OPENAI_API_KEY`가 있으면 OpenAI API로 기획/평가하고, 없으면 Codex CLI가 기획/병합도 수행합니다.

## 준비

루트에서 CLI를 빌드합니다.

```bash
go build -o jj ./cmd/jj
```

샘플 workspace를 git repo로 초기화합니다.

```bash
./playground/setup.sh
```

이 repo에는 API key 값을 담지 않는 예시 `.jjrc`가 포함되어 있습니다. OpenAI API를 쓰고 싶으면 실제 key는 환경 변수에만 둡니다. key가 없으면 Codex CLI가 설치되어 있어야 합니다.

```bash
export OPENAI_API_KEY=...
```

## dry-run 테스트

dry-run은 기획 산출물만 만듭니다. workspace 루트에 `SPEC.md`, `TASK.md`를 쓰지 않고, 구현 Codex 실행도 하지 않습니다. 단, `OPENAI_API_KEY`가 없으면 기획/병합을 위해 Codex CLI가 실행될 수 있습니다.
`playground/plan.md`는 현재 셸 위치 기준 경로이고, `--cwd`는 Codex가 수정할 대상 workspace만 지정합니다.

```bash
./jj run playground/plan.md --cwd playground/workspace --dry-run
```

확인할 것:

```bash
find playground/workspace/.jj/runs -maxdepth 3 -type f | sort
test ! -f playground/workspace/SPEC.md
test ! -f playground/workspace/TASK.md
```

## full-run 테스트

full-run은 `SPEC.md`/`TASK.md`를 workspace 루트에 쓰고, Codex가 샘플 Go 앱을 수정할 수 있습니다.

```bash
./jj run playground/plan.md --cwd playground/workspace
```

확인할 것:

```bash
git -C playground/workspace status --short
find playground/workspace/.jj/runs -maxdepth 3 -type f | sort
```

## 정리

실행 산출물을 지우려면:

```bash
rm -rf playground/workspace/.jj playground/workspace/SPEC.md playground/workspace/TASK.md
```

workspace git 상태를 샘플 초기 상태로 되돌리려면:

```bash
git -C playground/workspace reset --hard HEAD
git -C playground/workspace clean -fd
```
