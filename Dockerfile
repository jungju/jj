FROM golang:1.25.0-bookworm AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/jj ./cmd/jj

FROM node:24-bookworm-slim

RUN apt-get update \
	&& apt-get install -y --no-install-recommends bash ca-certificates git \
	&& rm -rf /var/lib/apt/lists/* \
	&& npm install -g @openai/codex \
	&& npm cache clean --force

COPY --from=builder /out/jj /usr/local/bin/jj

ENV JJ_CODEX_BIN=codex

WORKDIR /workspace

EXPOSE 7331

ENTRYPOINT ["jj"]
CMD ["serve", "--cwd", "/workspace", "--host", "0.0.0.0", "--port", "7331"]
