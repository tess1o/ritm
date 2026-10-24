# Ritm

Ritm (Ukrainian *ритм*, "rhythm") is a self-hosted, multi-user app for Apple Watch data. It ingests the Apple Health
`export.zip` and keeps a deduplicated history of daily activity, heart metrics, sleep and workouts. It shows dashboards
and generates plain-language insights. Uptime Kuma can poll it and alert you when new data stops arriving.

- One Go binary with the Vue SPA embedded, SQLite (WAL), no CGO, and no outbound network calls.
- Each user's data lives in its own SQLite file (`users/<uuid>.db`). `app.db` holds only the accounts, sessions and API
  keys.
- Dark, mobile-first UI that can be installed as a PWA.

- A read-only MCP server at `/mcp`, so Claude and other AI assistants can answer questions about your data.

## Quick start (Docker)

1. Create a folder for Ritm and its data directory. The image runs as the distroless `nonroot` user (uid 65532), so the
   data directory must be writable by that user:

   ```sh
   mkdir -p ritm/data && cd ritm
   sudo chown 65532:65532 data
   ```

2. Create `compose.yaml` in that folder:

   ```yaml
   services:
     ritm:
       image: ritm:latest            # build it with `make docker`
       container_name: ritm          # the CLI examples below use `docker exec ritm`
       restart: unless-stopped
       ports: [ "8080:8080" ]
       environment:
         RITM_INIT_USER: admin
         RITM_INIT_PASSWORD: change-me # pick a long password before the first start
         # RITM_REGISTRATION_ENABLED: "true"
         # RITM_BASE_URL: https://ritm.example   # only once it is served over HTTPS, see below
       volumes:
         - ./data:/data
       healthcheck:
         test: [ "CMD", "/ritm", "healthcheck" ]   # distroless has no curl; the binary probes /healthz
         interval: 30s
   ```

3. Start it:

   ```sh
   docker compose up -d
   ```

Then open `http://<host>:8080`, sign in and upload `export.zip`. To get the file on the iPhone, go to Health →
profile picture → *Export All Health Data* and save it to Files. iOS Safari can upload it from Files.

### Reverse proxy

Exports are 0.5–3 GB, so the proxy must allow large and slow uploads. With nginx:

```nginx
location / {
    proxy_pass http://127.0.0.1:8080;
    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    client_max_body_size 4g;
    proxy_request_buffering off;
    proxy_read_timeout 600s;
    proxy_send_timeout 600s;
    proxy_buffering off;          # import progress is streamed with SSE
}
```

Set `RITM_BASE_URL` to the public `https://` URL. It marks the session cookie `Secure`, so the browser never sends it
over plain HTTP. It is also the URL in the copy-ready snippets on the Settings and AI assistants pages (otherwise they
use the address the page was opened at), and an allowed `Origin` for writes when the proxy changes the `Host` header.
Leave it unset while you reach Ritm over plain `http://`: with an `https://` value the browser drops the cookie and
sign-in fails.

With the proxy on the same host, publish the port on loopback only (`ports: [ "127.0.0.1:8080:8080" ]`), so
Ritm can't be reached around the proxy. Docker's published ports bypass host firewalls such as ufw.

The login and API-key rate limits use the client IP. By default Ritm believes `X-Forwarded-For` only from
loopback, which covers nginx or Caddy running on the host. If the proxy runs in another container, set
`RITM_TRUSTED_PROXIES` to its Docker network (e.g. `172.18.0.0/16`). Ritm logs a warning the first time it ignores
the header from a private address. Private addresses aren't trusted by default because with some Docker
port-forwarding setups (IPv6, rootless Docker, swarm) every internet client arrives from one, and could then pick its
own IP. Login failures are also limited per username, whatever the IP.

Ritm compresses its responses (gzip) and sends `Strict-Transport-Security` when `RITM_BASE_URL` is `https://`, so
the proxy doesn't need to add either.

## Configuration

All settings are environment variables.

| Variable                                | Default            | Description                                                                                                                                         |
|-----------------------------------------|--------------------|-----------------------------------------------------------------------------------------------------------------------------------------------------|
| `RITM_INIT_USER` / `RITM_INIT_PASSWORD` | —                  | User created at startup if it does not exist yet (never overwritten). Set both or neither.                                                          |
| `RITM_REGISTRATION_ENABLED`             | `false`            | Allow visitors to create accounts on the login page.                                                                                                |
| `RITM_MAX_CONCURRENT_IMPORTS`           | `1`                | Server-wide cap on imports running at once, to bound CPU, RAM and disk I/O. Each user can run at most one; uploads beyond the cap wait as "queued". |
| `RITM_DATA_DIR`                         | `/data`            | Holds `app.db`, `users/<uuid>.db` and temporary uploads.                                                                                            |
| `RITM_LISTEN_ADDR`                      | `:8080`            | Listen address.                                                                                                                                     |
| `RITM_BASE_URL`                         | —                  | Public URL. `https://` makes the session cookie `Secure`; also used in copy-ready snippets. See [Reverse proxy](#reverse-proxy).                    |
| `RITM_TZ`                               | `UTC`              | Time zone for users who have none yet. Each user's zone is taken from the browser at the first sign-in and can be changed in Settings.             |
| `RITM_MAX_UPLOAD_MB`                    | `4096`             | Upload limit.                                                                                                                                       |
| `RITM_SESSION_TTL`                      | `720h`             | Session lifetime.                                                                                                                                   |
| `RITM_DELTA_WINDOW`                     | `168h`             | Window re-read before the high-water mark on delta imports.                                                                                         |
| `RITM_STALE_AFTER`                      | `336h`             | Default no-new-data threshold for monitoring. Each user can override it in Settings.                                                                |
| `RITM_LOG_LEVEL`                        | `info`             | `debug`, `info`, `warn` or `error`. Logs are JSON on stderr.                                                                                        |
| `RITM_TRUSTED_PROXIES`                  | loopback           | Peers whose `X-Forwarded-For` is believed: comma-separated IPs/CIDRs (e.g. `172.18.0.0/16`), or `none`.                                             |
| `RITM_SHORTCUT_URL`                     | the shared link    | iCloud link of the *Send to Ritm* iPhone shortcut offered under Settings → Connections. Set it to share your own build; must be `https://`.         |

Durations accept Go syntax (`72h`) plus a `d` unit (`14d`).

## Accounts

There is no admin UI. Accounts are managed with the CLI, which also works while the server runs:

```sh
docker exec -it ritm /ritm user list
docker exec -it ritm /ritm user add bob            # asks for the password
docker exec -it ritm /ritm user passwd bob
docker exec -it ritm /ritm user suspend bob        # ends sessions and disables API keys
docker exec -it ritm /ritm user unsuspend bob
docker exec -it ritm /ritm user delete bob         # account and data
```

Users can change their password, delete all their data or delete their account under Settings.

Other commands: `ritm import --user NAME [--full] FILE`, `ritm recompute --user NAME`,
`ritm backup DIR`, `ritm healthcheck` and `ritm version`.

## AI assistants (MCP)

Ritm serves the Model Context Protocol over streamable HTTP at `/mcp`. Clients authenticate with
`Authorization: Bearer <key>` using a `read` key, and each key reads only its owner's
database. The **AI assistants** page in the app walks through it: pick or create a key, then copy a
ready-made setup for Claude Code, Claude Desktop, VS Code, Cursor or another client. For example:

```sh
claude mcp add --transport http ritm https://ritm.example/mcp --header "Authorization: Bearer ritm_…"
```

Claude.ai web and mobile custom connectors require OAuth, which Ritm does not provide, so use a
desktop or CLI client.

| Tool                            | What it returns                                                                                                                      |
|---------------------------------|--------------------------------------------------------------------------------------------------------------------------------------|
| `get_overview`                  | Period summary with deltas vs the 4-week baseline, top insights, scores, data freshness                                              |
| `get_insights`                  | Insights with evidence; filter by period, category, polarity                                                                         |
| `get_daily_activity`            | Steps, distance, kcal, flights, exercise, stand, sedentary stretch, rings                                                            |
| `get_day_detail`                | One day: hourly steps, 15-minute HR curve, workouts, sleep stages                                                                    |
| `get_heart_metrics`             | `resting_hr`, `hrv`, `walking_hr`, `vo2max`, `hr_daily`, `spo2`, `resp_rate` or `wrist_temp`, with summary and optional rolling mean |
| `get_sleep`                     | Nightly sleep duration and stages                                                                                                    |
| `list_workouts` / `get_workout` | Workouts with HR, zones, load and RPE/notes; one workout with its HR series                                                          |
| `get_training_load`             | Daily load, CTL/ATL/TSB/ACWR, weekly consistency and streaks                                                                         |
| `compare_periods`               | Two periods side by side with deltas                                                                                                 |
| `get_settings`                  | Goals, HR zones in bpm, source priority                                                                                              |
| `query_sql`                     | One read-only `SELECT`/`WITH`, at most 500 rows, 5 s timeout. Off until the user enables it in Settings                              |

Resources: `ritm://schema` (tables, views and units for `query_sql`) and `ritm://data-coverage`.
Prompts: `weekly_review`, `monthly_review` and `training_review`.

Conventions:

- Dates are `YYYY-MM-DD` or names such as `last_30d`, `last_month` or `ytd`.
- Times are local `YYYY-MM-DD HH:MM`, and units are in the field names.
- Ranges longer than three months are returned per week or per month.
- Lists are capped and flagged `truncated`.

`query_sql` is off by default; each user can turn on read-only SQL queries in Settings → Connections. The switch
applies to all of that user's `read` keys. It runs on a read-only connection (`mode=ro`, `query_only`). A SQLite authorizer denies
everything except reading the user's own database, including `PRAGMA`, `ATTACH`, writes and
extension loading. Sorts and temporary results spill to a temporary file rather than memory.

`read` keys can't change anything. The same key also works for the REST API (see
[Reading your data from a script](#reading-your-data-from-a-script)). The MCP server is stateless, so every request
is tied to the key that sent it. Data an assistant reads becomes part of the conversation with that assistant's
provider.

## REST API

The web app, iPhone Shortcuts and Uptime Kuma all use one versioned REST API under `/api/v1`. Its
OpenAPI 3.1 description is generated from the Go handlers (Huma). It is committed as
[`docs/openapi.yaml`](docs/openapi.yaml) and served at `/api/v1/openapi.json`, with a browsable
reference at `/api/v1/docs`.

- Collections are `{"items": [...]}`. Ranged reads take `period` (e.g. `last_30d`, `2026-09-01..2026-09-30`)
  or `from`/`to`. Errors are RFC 9457 problem details.
- Browser sessions use the `ritm_session` cookie and must send `X-Ritm-CSRF: 1` on writes.
- API keys (`Authorization: Bearer ritm_…`, created under Settings → Connections) have one scope:
  `read` reads health data over REST and MCP, `ingest` uploads exports and reads their status, and
  `monitor` reads data freshness. No key can change data, settings or keys.

After changing a handler or a response type, run `make openapi`. It regenerates
`docs/openapi.yaml` and the frontend's TypeScript types (`web/src/lib/api-schema.d.ts`), and CI
fails when either is stale.

### Upload from an iPhone Shortcut

The **Send to Ritm** shortcut uploads `export.zip` from the Health share sheet. Setting it up needs
no typing:

1. Settings → Connections → *iPhone Shortcut*.
2. **Install the shortcut**: scan the first code with the iPhone (on the iPhone itself: *Get the
   shortcut*) → *Add Shortcut*.
3. **Connect it**: *Create key and connect* makes a key with scope `ingest` and shows a second code
   (on the iPhone: a *Connect shortcut* button). It is a
   `shortcuts://run-shortcut?name=Send%20to%20Ritm&input=text&text=…` link carrying
   `{"ritm":1,"url":"<server>/api/v1/imports","key":"ritm_…"}`. The shortcut checks it against the
   server, saves it as iCloud Drive → Shortcuts → `Ritm.json`, and shows *Connected to Ritm*. To
   switch servers or keys, generate a new key and scan again.
4. On the first run, allow the connection to your server and access to the folder (*Always Allow*).
   If large uploads are refused, turn on iOS Settings → Shortcuts → Advanced → *Allow Sharing Large
   Amounts of Data*.

Then, for each export: Health → profile picture → **Export All Health Data** → share sheet →
**Send to Ritm**. Stay in Shortcuts until the *import started* notification; progress shows on the
Import page. The shortcut keeps the last export as Shortcuts → `Ritm-export.zip`. Run it from the
Shortcuts app (no input) to resend it, or save exports under that name and let a nightly Personal
Automation send them. Sending the same export twice is harmless: Ritm stores only new data.

A self-hosted server cannot make a shortcut with its address and key built in: iOS only imports
signed shortcut files, and signing needs a Mac signed into iCloud. So one generic shortcut is shared
by iCloud link ([Send to Ritm](https://www.icloud.com/shortcuts/efcd38d390754327925c710a4c2c58e6),
`RITM_SHORTCUT_URL` to use another), and the connect link configures it.

#### Building the shortcut

For maintainers, or to build it yourself. Name it exactly **Send to Ritm** (the connect link runs it
by name). In its details, turn on *Show in Share Sheet*, receive **Files** and **Text**, and set
*If there's no input* to *Continue*.

Its files live directly in iCloud Drive → Shortcuts. **Save File** does not create folders, so give it
a full file name as the subpath, with *Ask Where to Save* off and *Overwrite If File Exists* on:
`Ritm.json` (connection) and `Ritm-export.zip` (last export). **Get File from Folder** reads them
from the Shortcuts folder with *Error If Not Found* off. When an **If** on a variable offers file
properties (*File Size*), tap the variable and set its type (Number or Text) first.

Actions:

1. **Get Type** of *Shortcut Input*. **If** *Type* is `Text` (configure):
   - **Get Dictionary from Input** (*Shortcut Input*).
   - **Get Dictionary Value** `url` → **Set Variable** `RitmURL`; **Get Dictionary Value** `key` →
     **Set Variable** `RitmKey`.
   - **Text** `RitmURL` + `/00000000-0000-0000-0000-000000000000` (no space between them) → **Get
     Contents of URL** *Text*, method GET, header `Authorization` = `Bearer ` + `RitmKey`.
   - **Get Dictionary Value** `status` in *Contents of URL*. Ritm answers an unknown import id with
     a problem JSON, so `404` means the server and key are fine.
   - **If** *Dictionary Value* is `404` — tap the variable and set its type to **Number** first, or
     Shortcuts treats it as a file and offers *File Size*:
     - **Set Name** of *Shortcut Input* to `config.json` → **Save File** *Renamed Item* to
       `Ritm.json` → **Show Notification** "Connected to Ritm".
     - **Otherwise** → **Show Notification** "Ritm connection failed (status: *Dictionary Value*)":
       `401` is a revoked or wrong key, empty means the server was not reached.

   Saving only after the check means a bad link never overwrites a working connection.
2. Otherwise (upload):
   - **Get File from Folder** `Ritm.json`. **If** *File* does not have any value → notify "Not
     connected. Open Ritm → Settings → Connections → iPhone Shortcut." → **Stop This Shortcut**.
   - **Get Dictionary from Input** *File* → **Get Dictionary Value** `url` → **Set Variable**
     `RitmURL`; `key` → `RitmKey`.
3. **Continue Shortcut in App**, where iOS offers it (it is missing on iOS 26). Share-sheet shortcuts
   get less memory; if a large export fails from the share sheet, save it as `Ritm-export.zip` and
   run the shortcut from the Shortcuts app instead.
4. **If** *Shortcut Input* has any value:
   - **Set Name** of *Shortcut Input* to `export.zip` → **Save File** *Renamed Item* to
     `Ritm-export.zip` → **Set Variable** `Export` to *Saved File*.
   - **Otherwise** → **Get File from Folder** `Ritm-export.zip`. If it has no value, notify
     "Nothing to send. Share an export from Health." and stop. **Set Variable** `Export` to *File*.
5. **Get Contents of URL** *RitmURL*, method POST. Headers: `Authorization` = `Bearer ` +
   *RitmKey*, `X-File-Name` = `export.zip`. Request body **File** = *Export*.
6. **Get Dictionary Value** `detail` in *Contents of URL*. **If** it has any value, the upload was
   refused (problem JSON, e.g. 401, 409 or 413): notify "Ritm: *detail*". **Otherwise** → **Get
   Dictionary Value** `status` → notify "Ritm: import *status*".
7. Optional: **Repeat** 90 times: **Wait** 10 s → GET `<url>/<id>` → once `status` is
   `succeeded`, notify "Ritm: N new workouts" from `stats.new_workouts` and stop. On `failed`,
   notify `error` and stop.

Share it (Share → Copy iCloud Link) and set the link as `RITM_SHORTCUT_URL`. A changed shortcut needs
a new link; the `Ritm.json` format stays the same across shortcut versions.

Behind a reverse proxy, allow large request bodies and slow uploads. nginx defaults to
`client_max_body_size 1m`. Cloudflare's free plan caps request bodies at 100 MB, so send exports
over a LAN or Tailscale address instead.

The same upload works with curl:

```sh
curl -H "Authorization: Bearer ritm_…" -H "X-File-Name: export.zip" \
  --data-binary @export.zip https://ritm.example/api/v1/imports
```

An `ingest` key cannot read health data, settings or keys.

To follow an import from a script, take its `id` from the response. Then poll the import, or stream
its `progress` events (the stream ends with `done`):

```sh
KEY=ritm_…
ID=$(curl -s -H "Authorization: Bearer $KEY" -H "X-File-Name: export.zip" \
  --data-binary @export.zip https://ritm.example/api/v1/imports | jq -r .id)

curl -s -H "Authorization: Bearer $KEY" https://ritm.example/api/v1/imports/$ID \
  | jq '{status, phase: .progress.phase, percent: .progress.percent}'
curl -N -H "Authorization: Bearer $KEY" https://ritm.example/api/v1/imports/$ID/events
```

Add `?full=true` to the upload to ignore earlier imports and re-read the whole file.

### Reading your data from a script

Create a key under Settings → Connections (*AI assistant or script*). It has the `read` scope and
works on every data read below. Send it as a bearer token:

```sh
KEY=ritm_…
curl -s -H "Authorization: Bearer $KEY" 'https://ritm.example/api/v1/overview?period=last_7d'
```

A read key can't change anything. Writes such as rating a workout need a browser session. To script those, sign in
like the browser does and keep the cookie. Sign-in and every write need `-H 'X-Ritm-CSRF: 1'`, or they fail with
`403` and `missing CSRF header or cross-origin request`:

```sh
curl -c ritm.cookies -H 'X-Ritm-CSRF: 1' -H 'Content-Type: application/json' \
  -d '{"username":"alice","password":"…"}' https://ritm.example/api/v1/auth/login
```

The most useful endpoints (all under `/api/v1`):

| Endpoint                        | Returns                                                                                                                                                    |
|---------------------------------|------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `GET /overview`                 | Period averages (steps, distance, kcal, exercise, stand, resting HR, HRV, sleep) with deltas vs the baseline, goals, scores, latest insights and freshness |
| `GET /activity/daily`           | Per day: steps, distance, kcal, flights, exercise and stand minutes, rings, steps by hour                                                                  |
| `GET /heart/daily`              | Per day: HR range, resting and walking HR, HRV, VO2max, SpO2, respiration, wrist temperature                                                               |
| `GET /sleep/nights`             | Per night: time in bed and asleep, core/deep/REM/awake minutes, bedtime and wake time                                                                      |
| `GET /workouts`                 | Workouts, newest first, with duration, distance, kcal, HR, % in zone 3+, load. Filter with `type` (see `GET /workout-types`) and `limit`                   |
| `GET /workouts/{id}`            | One workout: HR zones, events and pauses, RPE and notes, and the previous workout of the same type                                                         |
| `PUT /workouts/{id}/annotation` | Set RPE (1–10 or `null`) and notes. Both fields are required. Needs a session                                                                             |
| `GET /days/{day}`               | One day: hourly steps, HR curve, workouts, sleep stages. `day` is `YYYY-MM-DD`, `today` or `yesterday`                                                     |
| `GET /training/load`            | Daily load with fitness (CTL), fatigue (ATL), form (TSB) and ACWR                                                                                          |
| `GET /insights`                 | Insights. Filter with `category`, `polarity`, `kind`, and `latest=true` for the newest period per rule                                                     |
| `POST /api-keys`                | Create a key: `{"name": "…", "scope": "read" \| "ingest" \| "monitor"}`. The `secret` is shown only once. Needs a session                                 |

Ranged endpoints take `period` (`last_7d`, `last_<n>d`, `this_month`, `last_month`, `ytd`,
`YYYY-MM-DD` or `FROM..TO`) or `from`/`to`. Without them they use a default, such as the last
7 days for `/overview` and the last 90 days for most lists.

```sh
# This week vs your baseline
curl -s -H "Authorization: Bearer $KEY" 'https://ritm.example/api/v1/overview?period=last_7d' \
  | jq '{steps: .steps_per_day, resting_hr: .resting_hr_bpm}'
# {
#   "steps":      {"value": 9000, "baseline": 8000, "delta_pct": 12.5, "days": 7},
#   "resting_hr": {"value": 60,   "baseline": 62,   "delta_pct": -3.2, "days": 7}
# }

# One month of activity as CSV
curl -s -H "Authorization: Bearer $KEY" 'https://ritm.example/api/v1/activity/daily?period=2026-09-01..2026-09-30' \
  | jq -r '.items[] | [.day, .steps, .active_kcal, .exercise_min] | @csv'

# This year's runs
curl -s -H "Authorization: Bearer $KEY" 'https://ritm.example/api/v1/workouts?period=ytd&type=HKWorkoutActivityTypeRunning' \
  | jq -c '.items[] | {day, minutes: (.duration_s/60|round), distance_m, avg_hr_bpm, load}'

# Rate a workout (a write: session cookie from the sign-in above)
curl -b ritm.cookies -X PUT -H 'X-Ritm-CSRF: 1' -H 'Content-Type: application/json' \
  -d '{"rpe": 7, "notes": "Heavy legs"}' https://ritm.example/api/v1/workouts/195/annotation

# What needs attention right now
curl -s -H "Authorization: Bearer $KEY" 'https://ritm.example/api/v1/insights?latest=true&polarity=attention' \
  | jq -r '.items[].title'
```

The browsable reference at `/api/v1/docs` lists every endpoint and field.

### Workouts for other apps

Apps that show workouts next to their own data, for example on a map or a timeline, can use a
`read` key and these parts of the workout API:

- **Identity.** Each workout has a `key` (`activity_type|start_ms|end_ms`). Unlike `id`, it stays the same after a data
  reset.
- **Kind.** `category` is one of `walk`, `run`, `hike`, `cycle`, `swim`, `row`, `paddle`, `sail`, `ski`, `strength`,
  `cardio` or `other`. `distance_based` says whether the activity covers a distance.
- **Location.** For workouts with a GPS route, `route` has the start and end points, the bounds and the number of points.
  It is `null` otherwise.
- **Selection.**
  - `start`/`end` (RFC 3339) match workouts overlapping an exact window, instead of `period`/`from`/`to`.
  - `category`, `type`, `near=lat,lon&radius_m=` (where the route starts) and `bbox=minLon,minLat,maxLon,maxLat` (what
    the route crosses) filter the list.
  - Pages are newest first: pass `next_cursor` as `cursor`.
- **Tracks.** `GET /workouts/routes` takes the same filters and returns many downsampled tracks at once as
  `[lat, lon, t_ms]` arrays. `GET /workouts/{id}/route` has pace and elevation for one workout.
- **Caching.** Data reads send an `ETag`; revalidate with `If-None-Match` and get `304` while nothing changed.
  `GET /freshness` adds `data_version`, which goes up with every change, and when the first and last workouts are.
- **Revoked keys.** A revoked key is rate-limited on its own, so it doesn't block other keys used from the same server.

```sh
# Runs and walks that started within 200 m of a place, last year
curl -s -H "Authorization: Bearer $KEY" \
  'https://ritm.example/api/v1/workouts?period=last_365d&near=50.4501,30.5234&radius_m=200' \
  | jq -c '.items[] | {key, category, day, start: .route.start}'

# Tracks of one week, 150 points each
curl -s -H "Authorization: Bearer $KEY" \
  'https://ritm.example/api/v1/workouts/routes?start=2026-10-12T00:00:00Z&end=2026-10-19T00:00:00Z'
```

## Monitoring with Uptime Kuma

Ritm sends no notifications. It exposes two endpoints, and Uptime Kuma decides when to alert and
delivers the alert through its own channels (Telegram, e-mail, ntfy, …).

**`GET /api/v1/monitor/freshness`**. Requires `Authorization: Bearer <key>` with a `monitor`-scoped key.
Create one under Settings → API keys. It reports on the key owner's data only.

| State                                           | HTTP          | `status`  |
|-------------------------------------------------|---------------|-----------|
| Newest Watch data is younger than the threshold | `200`         | `ok`      |
| No new data for the threshold or longer         | `503`         | `stale`   |
| Nothing imported yet                            | `503`         | `no_data` |
| Database error                                  | `500`         | `error`   |
| Missing or invalid key / key with another scope | `401` / `403` | —         |
| 20 invalid keys from one IP within 15 min       | `429`         | —         |

```json
{
  "status": "stale",
  "message": "No new Apple Health data for 16 days",
  "last_data_at": "2026-10-06T21:14:00+03:00",
  "age_days": 16.1,
  "stale_after_days": 14,
  "last_import_at": "2026-10-07T09:02:11+03:00",
  "checked_at": "2026-10-22T12:00:00+03:00"
}
```

The clock is the time of the newest sample, not the upload time, so uploading an old export again
does not reset it. Add `?max_age=10d` (or `72h`) to use a different threshold for one monitor. The
body contains only dates and counts, never health values.

**`GET /healthz`** needs no auth. It returns `200 {"status":"ok","version":"…"}` when the server and
database respond, and `503` otherwise.

Recommended monitors (the Settings page shows the same table with your URL):

| Monitor               | Type    | URL                                             | Settings                                                                                     |
|-----------------------|---------|-------------------------------------------------|----------------------------------------------------------------------------------------------|
| Ritm — data freshness | HTTP(s) | `https://ritm.example/api/v1/monitor/freshness` | Header `{"Authorization": "Bearer ritm_…"}`, interval 1 h, retries 0, attach a notification. |
| Ritm — up             | HTTP(s) | `https://ritm.example/healthz`                  | Interval 1–5 min, retries 2.                                                                 |

Kuma's default accepted status codes (200–299) work without changes. For stricter checking, use the
*HTTP(s) – Json Query* monitor type with expression `status` and expected value `ok`. Settings also
shows the current state and when Kuma last polled.

## Backup and restore

`ritm backup DIR` writes consistent copies of `app.db` and every user's database into a new or empty
directory. It mirrors the data-directory layout and uses SQLite `VACUUM INTO`, so it is safe while the
server runs, even during an import. Files are written with mode `0600`.

```sh
docker exec ritm /ritm backup /data/backups/2026-10-22
# or, from a source checkout with .env:  make backup DIR=backups/2026-10-22
```

Restore:

```sh
docker compose stop ritm
mv data data.old && mkdir data
cp -a data.old/backups/2026-10-22/. data/       # app.db and users/*.db
sudo chown -R 65532:65532 data
docker compose start ritm
```

You can also stop the container and copy the whole data directory. `app.db` and `users/` must come
from the same backup, because each account points to its database file by name. Any `-wal` and
`-shm` files next to a database belong to it, so copy them along when the server was not stopped
cleanly.

## Building

Requirements: Go ≥ 1.26 and Node ≥ 20.19.

```sh
make build              # npm ci + vite build, then a static binary with the UI embedded: bin/ritm
make docker             # image for this machine (IMAGE=ritm)
make docker-multiarch   # linux/amd64 + linux/arm64 with buildx; PUSH=1 IMAGE=ghcr.io/you/ritm pushes
```

The image is `gcr.io/distroless/static-debian12:nonroot` plus the binary. The Node and Go stages
run on the build platform and cross-compile, so multi-arch builds need no emulation.

CI (`.github/workflows/ci.yml`) runs `go vet`, gofmt, the Go tests (including the OpenAPI drift check), the
generated-types check, the frontend type check, lint, build and bundle budget, and checks that the image is ≤ 30 MB. It
builds both architectures and pushes them to GHCR on `v*` tags.

## Development

```sh
make dev        # Go API on :8080 + Vite on :5173 (creates .env from .env.example)
make seed       # import 180 days of synthetic data for the .env user
make test       # Go tests
make test-web   # type check, lint, build, bundle size
make lint       # go vet + gofmt
make openapi    # regenerate docs/openapi.yaml and the frontend API types
```