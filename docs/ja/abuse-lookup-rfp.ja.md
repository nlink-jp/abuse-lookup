# RFP: abuse-lookup

> Generated: 2026-07-14
> Status: Draft

## 1. Problem Statement

セキュリティ調査・インシデント対応の現場では、遭遇した IP アドレスの**悪用実績・
評判**（abuse confidence score、通報履歴、用途種別 (usage type)、ISP、国）を
素早く知りたい場面が多い。AbuseIPDB はこれを提供する定番サービスだが、都度 Web
UI を開くのは非効率で、AI ツール（Claude Code 等）から MCP 経由で調査させる手段
もない。

そこで **AbuseIPDB API v2 をラップし、少数の IP について実際に調査が必要になった
時点でレピュテーション情報をコマンド一発／MCP ツールで引ける CLI 兼ローカル MCP
サーバー** `abuse-lookup` を用意する。対象ユーザーは org 運営者本人と、その AI
ツール（MCP クライアント）。同じ IP を短時間に何度も引く場面があり得るため、
TTL 付きローカルキャッシュで重複問い合わせと無料枠（1,000 checks/日）の浪費を
抑える。オフライン DB を持つ [asn-lookup](../../../util-series/asn-lookup)（AS・国）
とは**別々に呼び出して**突き合わせることで、IP を多面的に調査する。

## 2. Functional Specification

### Commands / API Surface

単一バイナリ + サブコマンド構成（util-series 標準スタイルを踏襲。`mcp` サブコマンドで
MCP サーバー化）。

| コマンド | 役割 |
|---|---|
| `abuse-lookup check <IP>...` | IP→ レピュテーション情報。複数 IP 可、引数省略時は stdin から読む。`--max-age <days>`（通報の遡及日数、既定 90）、`--verbose`（最近の通報明細も含む）、`--refresh`（キャッシュを無視して再取得）、`-j` / `--json`（JSONL） |
| `abuse-lookup reports <IP>` | 通報明細（ページネーション）を取得（**Phase 2**）。大きい結果はファイル媒介 |
| `abuse-lookup cache` | キャッシュの件数・鮮度を表示。`--clear` で破棄 |
| `abuse-lookup doctor` | API キー設定・キャッシュ状態・API 疎通を診断 |
| `abuse-lookup mcp` | ローカル MCP サーバー（stdio）として起動 |
| `abuse-lookup version` | バージョン（`git describe`） |

**MCP ツール**（`abuse-lookup mcp`）:

| ツール | 役割 |
|---|---|
| `check_ip` | IP → レピュテーション情報（score / 国 / ISP / usageType / 通報総数 / 最終通報日 等） |
| `get_reports` | IP → 通報明細（**Phase 2**、大きい結果はファイル媒介） |
| `cache_status` | キャッシュの件数・鮮度を確認 |
| `get_usage` | 埋め込みマニュアル（asn-lookup 準拠。`initialize` の `instructions` から誘導） |

### Input / Output

- 既定は人間向けの整形テーブル出力。`-j` / `--json` で **JSONL**（1 レコード 1 行、
  pipe / 機械処理向け）。MCP は常に構造化 JSON を返す。
- stdin 対応（`cat ips.txt | abuse-lookup check`）で util 系の pipe 哲学に沿わせる。
- `check` の主フィールド: `ipAddress`, `abuseConfidenceScore`, `countryCode`,
  `usageType`, `isp`, `domain`, `totalReports`, `numDistinctUsers`,
  `lastReportedAt`, `isWhitelisted`, `isPublic`, `isTor`。
- **レート制限**: レスポンスの `X-RateLimit-Remaining` を surface し、上限接近時は
  警告表示。**429（日次上限超過）は明確なエラーで返す**（自動リトライはしない
  — 待っても日次リセットまで回復しないため）。
- **キャッシュ命中時**は API を叩かず、キャッシュ由来である旨（取得時刻）を示す。

### Configuration

秘密値は非コミット。sectioned TOML を採用（asn-lookup / lite-series 規約に準拠）。

- API キー: `ABUSEIPDB_API_KEY` 環境変数 ＞ `~/.config/abuse-lookup/config.toml` の
  `[abuseipdb] key = "..."`
- キャッシュ TTL: `[cache] ttl_hours = 12`（既定 12 時間。`--refresh` で個別に無視）
- キャッシュ保存先: 既定 `~/.local/share/abuse-lookup/cache/`

### External Dependencies

- 実行時の外部サービスは **AbuseIPDB API v2** のみ
  （`https://api.abuseipdb.com/api/v2/check` ほか。`Key:` ヘッダで API キーを毎回送信）。
  asn-lookup と異なり **クエリ時にネットワーク必須**（オフライン不可）。
- Go 標準ライブラリのみ: `net/http`（API 呼び出し）、`net/netip`（IP 検証）、
  `encoding/json`（レスポンス / キャッシュ）、`crypto/*` 不要。**外部依存ゼロ**。
- キャッシュ: IP ごとの JSON ファイル。**temp 書き込み → `rename` のアトミック
  ライト**（`os.Root` で containment、破損キャッシュを掴まない）。キャッシュキーは
  `IP + max-age + verbose` の組（パラメータ差で別レスポンスになるため）。

## 3. Design Decisions

- **Go 採用**: 単一バイナリ配布、macOS 署名 + notarize フロー確立済み。`net/netip`
  で IP 検証、`net/http` + `encoding/json` で API を標準ライブラリだけで扱える。
  **外部依存ゼロ**（asn-lookup / nlk / mcp-guardian と同方針）。
- **エンジン共有**: config + HTTP クライアント + キャッシュを束ねる engine を CLI と
  MCP で共有し、両者の挙動が乖離しないようにする（asn-lookup パターン）。
- **HTTP クライアントは interface 化**してテストでモック（実 API を叩かずにテスト
  可能に）。asn-lookup の `Fetcher` interface と同じ設計。
- **API キーは秘匿**: `Key:` ヘッダで毎回送信するが、ログ・エラーには絶対に出さない
  （`Redact` 相当）。
- **補完する既存ツール**: オフラインの asn-lookup（AS・国）と対をなす**評判レイヤー**。
  cybersecurity-series（ai-ir2 / mail-analyzer / cti-graph / ioc-collector）の IOC
  エンリッチメント基盤として、collector が集めた IP に abuse スコアを付与する用途。
  個別呼び出しで、abuse-lookup は asn-lookup に依存しない。
- **Out of scope**:
  - `blacklist`（悪性 IP 一括ダウンロード）— 少数 IP の調査が目的で、一括取得は非対象
  - `check-block`（CIDR 単位チェック、有料機能）
  - **通報系（`report` / `bulk-report` / `clear-address`）** — 共有 DB への書き込み
    ＝副作用のある外向き操作。v1 は調査（read-only）専用。将来入れるとしても MCP で
    AI に握らせず CLI のみ・明示ゲートとする
  - reverse DNS / PTR / whois / RDAP（asn-lookup 側・別ツールの領分）
  - キャッシュ（＝AbuseIPDB データ）の再頒布（ローカル private のみ）

## 4. Development Plan

### Phase 1: Core

- `net/netip` ベースの IP 検証、`net/http` + `Key:` ヘッダの AbuseIPDB `check`
  クライアント（interface 化）、レスポンスの構造体マッピング。
- TTL キャッシュ（IP ごと JSON、アトミックライト、`os.Root` containment、
  キー = `IP + max-age + verbose`）。
- `check`（複数 IP / stdin / `--max-age` / `--verbose` / `--refresh` / `-j`）、
  `doctor`、`cache` サブコマンド。
- config（sectioned TOML: `[abuseipdb] key` / `[cache] ttl_hours`）、env 上書き、
  API キー redaction。
- レート制限ヘッダ surface + 429 明示エラー。
- pure 関数中心・注入可能な依存（HTTP は interface 化してテストでモック）。
  実 API を叩かずにテスト。**独立レビュー可**。

### Phase 2: Features

- `mcp` サブコマンド（stdio MCP: `check_ip` / `cache_status` / `get_usage`）。
  data-toolbox-mcp の骨格を移植。
- `reports`（ページネーション通報明細）を CLI + `get_reports` MCP ツールで追加。
  大きい結果は **ファイル媒介**（agent 提供 `workspace_root`、asn-lookup パターン）。
- JSONL 出力・stdin の仕上げ、鮮度 / キャッシュ警告表示。
- **独立レビュー可**。

### Phase 3: Release

- README.md / README.ja.md / CHANGELOG.md / AGENTS.md 整備、LICENSE（MIT）。
- **AbuseIPDB クレジット / 利用規約の帰属表記** を出力および README に明記。
- Makefile（`make build` / `make build-all`）、署名 + notarize、GitHub リリース、
  umbrella submodule ポインタ更新、org profile 追加、`check-org.sh`。

## 5. Required API Scopes / Permissions

- AbuseIPDB 無料アカウントの **API キー 1 個**のみ。
- OAuth・特権 IAM ロール不要。キーは `check` / `reports` の照会にのみ使用する
  （通報系エンドポイントは叩かない）。

## 6. Series Placement

Series: cybersecurity-series
Reason: abuse confidence score / 通報履歴は評判・脅威インテリジェンスそのものであり、
ai-ir2 / mail-analyzer / cti-graph / ioc-collector の **IOC エンリッチメント基盤**
として括るのが自然。オフラインの asn-lookup（util-series）と対をなすが、abuse-lookup
はセキュリティ固有の評判レイヤーであるため cybersecurity-series に配置する。
ユーザー確定済み。

## 7. External Platform Constraints

- AbuseIPDB **無料枠は 1,000 checks/日**（超過で HTTP 429、日次リセット）。
  `X-RateLimit-Remaining` ヘッダを surface し、上限接近を警告。自動リトライはしない。
- レスポンスは **AbuseIPDB API v2 スキーマ**に依存（`data.abuseConfidenceScore` ほか）。
- 利用規約上、**出力元 = AbuseIPDB のクレジット表記**を README に必須で明記。
  取得データ（＝キャッシュ）はローカル private のみで、**再頒布しない**。
- API キー必須。**クエリ時にネットワーク必須**（オフライン不可、asn-lookup と対照的）。

---

## Discussion Log

- **着想**: AbuseIPDB API を使い、遭遇した少数 IP のレピュテーションを CLI / MCP から
  引くツールとして発案。asn-lookup の姉妹品（目的は「AI ツールからの IP 調査」で
  共通）だが、オフライン DB ではなく**オンライン REST API・API キー毎回送信・
  レート制限あり**という点で構造が根本的に異なると整理。
- **過剰コール回避策の違い**: asn-lookup はバルク DB を持つことで実現したが、
  abuse-lookup は**リアルタイム性が要るため TTL ローカルキャッシュ**で実現する
  （同一 IP の重複問い合わせと日次上限の浪費を抑える）と決定。
- **スコープ確定（調査 read-only）**: `check` を中心に据え、`blacklist` 一括 DL /
  `check-block` は非対象。**通報系（`report` 他）は共有 DB への書き込み＝副作用の
  ある外向き操作のため Out of scope** と確定。v1 は調査専用。
- **`reports`**: `check --verbose` でも直近の通報は取れるが、ページネーション付きの
  専用取得は **Phase 2** に配置。MCP では大きい結果をファイル媒介にする。
- **キャッシュ設計**: TTL は config で設定可（`[cache] ttl_hours`、既定 12 時間）、
  `--refresh` で個別無視。保存は IP ごとの JSON で、**アトミックライト（temp →
  rename）** を前提に破損キャッシュを掴まない構造とする。キーは
  `IP + max-age + verbose` の組。
- **レート制限方針**: `X-RateLimit-Remaining` を surface して上限接近を警告し、
  429 は明確なエラーで返す。**待っても日次リセットまで回復しない**ため自動リトライは
  しない、と判断。
- **外部依存ゼロ**: `net/http` + `net/netip` + `encoding/json` のみ。HTTP クライアントは
  interface 化してテストでモック（asn-lookup の `Fetcher` と同じ）。
- **系列**: util-series（asn-lookup と並べる）と cybersecurity-series（IOC エンリッチ
  基盤）で検討。評判/脅威インテリジェンス固有の性格を重視し **cybersecurity-series**
  に確定。
- **ツール名**: `abuse-lookup` に決定（`ip-reputation` / `abuseipdb-cli` も候補）。
  asn-lookup と対になる `<thing>-lookup` パターン。
