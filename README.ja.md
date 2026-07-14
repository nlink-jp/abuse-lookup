# abuse-lookup

[AbuseIPDB](https://www.abuseipdb.com) API を使って IP アドレスの評判を調べる
CLI **兼**ローカル MCP サーバー。IP の abuse confidence score・通報履歴・用途種別
(usage type)・ISP を照会し、結果を TTL 付きでローカルキャッシュするので、同じ IP を
何度引いても無料枠の日次上限を無駄に消費しません。

オフラインで AS/国を答える
[asn-lookup](https://github.com/nlink-jp/asn-lookup) の、オンライン・評判特化の
姉妹品です。両者を併用して IP を多面的に調査できます。

- **IP → 評判**: abuse confidence score・国・用途種別・ISP・ドメイン・通報総数・
  通報者数・最終通報日・whitelist / Tor フラグ。
- **通報履歴**: IP に対する個別の通報明細（ページネーション）。
- **CLI + MCP**: 同一エンジンが対話 CLI と stdio MCP サーバー
  （`check_ip` / `get_reports` / `cache_status` / `get_usage`）を駆動。
- **TTL キャッシュ**: `(IP, max-age, verbose)` 単位でキャッシュ。鮮度内のヒットは
  API クォータを消費しません。
- **外部依存ゼロ**: 標準ライブラリのみ（`net/http` + `net/netip` + `encoding/json`）。

> **データ:** AbuseIPDB (<https://www.abuseipdb.com>)。結果を提示する際は
> AbuseIPDB への**クレジット表記が必須**です。本ツールは各自のキーで API を照会し、
> キャッシュはローカルに保持するだけで**再頒布しません**。

## インストール

```bash
make build          # → dist/abuse-lookup（macOS では署名付き）
```

Go 1.25+ が必要です。全プラットフォーム向けは `make build-all`。

## クイックスタート

1. <https://www.abuseipdb.com/account/api> で**無料**の API キーを取得（無料枠は
   1,000 checks/日）。
2. キーを設定して IP を照会:

   ```bash
   export ABUSEIPDB_API_KEY=your_key_here
   abuse-lookup check 118.25.6.39
   #   Abuse score:     100/100 (high risk)
   #   Country:         CN
   #   Usage type:      Data Center/Web Hosting/Transit
   ```

3. 個別の通報を取得:

   ```bash
   abuse-lookup reports 118.25.6.39
   ```

## コマンド

| コマンド | 説明 |
|---------|-------------|
| `abuse-lookup check <IP>...` | IP ごとに評判を照会。引数省略時は stdin から読む。 |
| `abuse-lookup reports <IP>...` | IP ごとに通報明細を 1 ページ取得。 |
| `abuse-lookup doctor` | API キー設定・キャッシュ状態を診断（クォータ消費なし）。 |
| `abuse-lookup cache` | キャッシュ状態表示。`--clear` で破棄。 |
| `abuse-lookup mcp` | ローカル MCP サーバー（stdio）として起動。 |
| `abuse-lookup version` | バージョンとデータ帰属を表示。 |

### check フラグ

- `--max-age <days>` — 通報の遡及日数（既定 90）。
- `--verbose` — 最近の通報明細も含める。
- `--refresh` — キャッシュを無視して再取得。
- `-j` / `--json` — **JSON Lines**（1 IP 1 オブジェクト、pipe 向け）。

### reports フラグ

- `--max-age <days>` — 通報の遡及日数（既定 90）。
- `--page <n>` / `--per-page <n>` — ページネーション（既定 page 1・25 件/ページ）。
- `-j` / `--json` — JSON Lines（1 通報 1 行、`ipAddress` 付き）。

### 出力形式

- 既定: IP ごとの人間向けブロック。
- `-j` / `--json`: pipe 向けの **JSON Lines**。

```bash
cat ips.txt | abuse-lookup check --json | jq 'select(.abuseConfidenceScore >= 75) | .ipAddress'
```

不正な IP や個別 IP の API エラーは行単位で報告し、バッチを中断しません。レート制限
（日次上限）に達した場合はバッチを停止します（リセットまで回復しないため）。

## MCP サーバー

MCP クライアントに登録します。Claude Code の場合:

```bash
claude mcp add abuse-lookup -- /path/to/abuse-lookup mcp
```

クライアント設定の場合:

```json
{
  "mcpServers": {
    "abuse-lookup": { "command": "/path/to/abuse-lookup", "args": ["mcp"] }
  }
}
```

ツール（最初に `get_usage` を呼ぶとマニュアルとリカバリ表が得られます。`initialize`
の `instructions` フィールドでも案内します）:

| ツール | 引数 | 用途 |
|------|-----------|---------|
| `get_usage` | — | マニュアル: ツール・キャッシュ・ワークスペース・リカバリ表 |
| `check_ip` | `ip` (string) / `ips` (array), `max_age`, `verbose`, `refresh` | IP → 評判（キャッシュ） |
| `get_reports` | `ip`, `max_age`, `page`, `per_page`, `limit`, `workspace_root`, `workspace_id` | IP → 通報明細（大きいページはファイル媒介） |
| `cache_status` | — | キャッシュのディレクトリ・TTL・件数 |

**キャッシュ.** `check_ip` の結果は `(IP, max_age, verbose)` 単位で設定 TTL（既定
12h）だけキャッシュされ、`cached: true` として**クォータを消費せず**返されます。
`refresh: true` でライブ取得を強制できます。

**大きい通報ページはファイル媒介.** `get_reports` は常にページメタ情報（`total` /
`page` / `count` / `has_next_page`）とインラインプレビューを返し、ページが `limit`
（既定 25）を超えるとフルページをファイルに書き出しパスのみ返します（`reports_file` /
`truncated: true`）。出力先は呼び出し側が用意します: 自分のファイルツールで
ディレクトリを作り `workspace_root` に渡してください（サンドボックス環境で必須）。
省略時はサーバー既定を使用。書き込みは `os.Root` でワークスペースに封じ込め
（仕込まれた symlink は脱出不可）。

## 設定

設定は次の順で解決（後勝ち）: config ファイル → 環境変数 → フラグ。

**API キー**（必須）:

- `ABUSEIPDB_API_KEY`（または `ABUSE_LOOKUP_KEY`）環境変数、または
- config ファイルの `[abuseipdb] key`。

キーは意図的にコマンドラインフラグにしていません（シェル履歴・プロセス一覧に残さない
ため）。`Key` リクエストヘッダで送信し、URL・ログ・エラーには一切出しません。

**config ファイル** — `~/.config/abuse-lookup/config.toml`
（`XDG_CONFIG_HOME` を尊重）。[`config.example.toml`](config.example.toml) 参照:

```toml
[abuseipdb]
key = "your_key_here"
# base_url = "https://api.abuseipdb.com/api/v2"

[cache]
# ttl_hours = 12
# dir = "~/.local/share/abuse-lookup/cache"

# [mcp]
# get_reports のファイル媒介結果の既定出力先（ABUSE_LOOKUP_WORKSPACE）。
# 呼び出しごとに workspace_root で上書き可能。
# workspace = "~/.local/state/abuse-lookup/workspace"
```

**キャッシュ保存先** — `~/.local/share/abuse-lookup/cache`
（`XDG_DATA_HOME` を尊重）。

## レート制限

AbuseIPDB の無料枠は **1,000 checks/日**（日次リセット）。ライブの `check` /
`reports` は 1 件消費し、キャッシュヒットは消費しません。残枠が少なくなると stderr に
警告を表示し、`429`（上限超過）は明確なエラーで返します（自動リトライなし）。

## しくみ

`check` は `net/netip` で IP を検証し、ローカル TTL キャッシュを参照、ミス時に
`Key` ヘッダ付きで `GET /check` を照会します。レスポンスは IP ごとの JSON ファイルへ
アトミック（temp + rename、`os.Root` 封じ込め）に書き込むので、破損キャッシュを
掴みません。`reports` は `GET /reports`（ページネーション）を照会し、キャッシュ
しません。CLI と MCP サーバーは同一エンジンを共有し、挙動が乖離しません。
[docs/ja/architecture.ja.md](docs/ja/architecture.ja.md) 参照。

## 開発

```bash
make test     # go test -race -cover ./...
make build    # → dist/abuse-lookup
make check    # lint + test + build-all
```

## ライセンス

コード: [MIT](LICENSE)。データ: AbuseIPDB — クレジット必須。キャッシュはローカルで
再頒布しません。
