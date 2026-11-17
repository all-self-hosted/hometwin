# HomeTwin

A live 3D twin of a home. The walls are cut at 1.2 m so you can see into every room. You paste an IKEA product link, the server reads the real size from the page, and a model of that size goes into the room. Or you say what you want where (卧室A 放一个床头柜) and pick one of the free models found in Polyfork's catalogue. The smart devices in the model (lights, ACs, door lock, curtains, robot vacuum, sensors) show their live state and can be controlled from it.

It ships with one sample flat: a 89 m² two-bedroom in Chengdu. Its devices are simulated until you connect Home Assistant: HA entities can then be mapped onto devices in the rooms, their state shows live in the model, and commands from HomeTwin go to HA after HomeTwin's own rules and approvals.

The home is also kept as a knowledge graph with an ontology in CortexDB (rooms, furniture, devices, what is where and what controls what, inference rules and SHACL constraints), which answers questions such as 客厅有哪些设备. The 家庭助手 is an agent-go assistant with tools over the home; anything risky it is asked to do, such as unlocking the front door, waits for a person to approve.

## Run

With Docker (amd64 or arm64, a Raspberry Pi or a NAS is fine):

```sh
docker run -d --name hometwin -p 6417:6417 -v hometwin:/data \
  -e HOMETWIN_PASSWORD=change-me ghcr.io/all-self-hosted/hometwin
```

Open http://<that machine>:6417. Settings below go in as `-e` variables; for Home Assistant add `-e HA_URL=http://<ha>:8123 -e HA_TOKEN=<long-lived token>`. Images are tagged by release (`0.1.0`, `0.1`, `latest`) and `edge` follows `main`. Blender is not in the image; the browser builds the furniture models instead.

With Home Assistant OS or Supervised, install the add-on instead: add `https://github.com/all-self-hosted/hometwin-addon` under Settings → Add-ons → Add-on store → ⋮ → Repositories. It opens in HA's sidebar and connects to that HA by itself, with no token to make.

Ready-made binaries for Linux, macOS and Windows are on the releases page; run one with `-http 0.0.0.0:6417` to reach it from other machines.

From source you need Go 1.27, Node 22 or newer, and optionally Blender (it generates the furniture models; without it the browser builds its own).

```sh
scripts/build.sh                      # builds web/ and embeds it in server/bin/hometwin
BLENDER=/Applications/Blender.app/Contents/MacOS/Blender \
  server/bin/hometwin -http 127.0.0.1:6417 -data data
```

Open http://127.0.0.1:6417.

To work on the front end, run the server as above, then run `cd web && npm run dev` and open http://127.0.0.1:6416. Vite sends `/api` through to the server.

## Settings

| | |
|---|---|
| `-http` | address to serve on (default `127.0.0.1:6417`) |
| `-data` | layouts, devices, automations and generated models (default `./data`) |
| `-mqtt` | built-in MQTT broker (default `127.0.0.1:47183`, empty turns it off). Device state is published retained on `home/<domain>/<room>/<device>`, and changes are sent to the same topic plus `/set` |
| `-speed` | runs the simulated home faster than real time |
| `-vr-import` | turns on importing 贝壳/如视 VR scans (off by default, see below) |
| `HOMETWIN_PASSWORD` or `HOMETWIN_PASSWORD_FILE` | a password in front of the whole UI and API (the file's first line is read). Without one anyone who can reach the address can use it, and the server warns when it listens beyond this machine. The sign-in lasts 180 days, signed with a key kept in `<data>/session.key`; changing the password signs everyone out. The MQTT broker has no password, so keep `-mqtt` on 127.0.0.1 or turn it off when the machine is reachable |
| `HTTPS_PROXY` | proxy used to fetch product pages |
| `BLENDER` | path to the Blender binary |
| `HA_URL` | Home Assistant's address (default `http://127.0.0.1:38123`; can also be set in the 接入 Home Assistant panel) |
| HA token | on macOS from the Keychain: `security add-generic-password -a hometwin -s hometwin-ha -w` (it prompts for the token); elsewhere `HA_TOKEN`, or a file named by `HA_TOKEN_FILE` with a `HA_TOKEN=` line |
| `LLM_BASE_URL`, `LLM_API_KEY`, `LLM_MODEL` | the model for the 家庭助手 (any OpenAI-compatible API). Without them the assistant panel says so, and everything else works |

## Tests

```sh
cd server && go test ./...            # add -short to skip the Blender builds
cd web && npm run typecheck && npm run build
```

The IKEA parser tests run against saved product pages in `server/internal/ikea/testdata` (their sources are listed in `SOURCES.txt`).

## Importing a floor plan

导入户型图 (top bar) takes a 链家/贝壳-style plan (JPG or PNG, a phone screenshot is fine: the app bars around the plan are cut off) and turns it into the home's shell. It needs the model from `LLM_*` to accept images. The model reads the dimension chains, room names, printed areas, doors and windows; the scale and positions come from the image itself: the chains' tick marks are fitted to the printed millimetres, and every room edge is put on the wall stroke nearest where the model drew it, then on a chain's grid line when one is close. Doors and windows go into the openings in the wall strokes. The draft is checked (each room's area against the printed one, overlaps and gaps, which rooms each door joins), and an agent-go agent works through what the checks find, looking again at crops of the plan before changing anything. A model call takes about a minute, the whole import one to five.

The draft is shown over the picture; 用这个户型 saves it as `data/home.json` and the server restarts on it (layouts and devices are kept per home id, so the sample's stay as they were). 换回示例户型 in the same dialog goes back. Each import's upload, the model's raw answer and the draft are kept in `data/floorplan/<id>/`.

The deterministic part is tested on a made-up plan drawn by the test, and on a real plan with a recorded answer when one is saved in `server/internal/floorplan/testdata/private/` (kept out of git). To record a new one, or read a plan live: `FLOORPLAN_LIVE=plan.jpg FLOORPLAN_OUT=reading.json go test ./internal/floorplan -run Live -v`; `FLOORPLAN_DEBUG=out.png go test ./internal/floorplan -run DebugRender` draws a draft over its picture.

## Importing a VR scan

This is experimental and off unless the server is started with `-vr-import`. The scan, its textures and plan belong to Realsee/贝壳; only import scans of your own home, for your own use.

导入户型 → 贴 VR 看房链接 takes a 贝壳/如视 share link (paste the whole shared text). The server reads the share page, downloads the textured scan (an LZMA-packed protobuf mesh, about 1 MB, plus four textures) and works out the plan from the mesh itself: it cuts the walls at 2.2 m over the local floor (or just under a lower ceiling) to close the doors, at the sills to close the windows, fills the rooms in between, and finds the doors and windows by cutting each wall every 5 cm of height. Upward faces give each room's floor height (raised bedrooms, a bathroom a step down, as `Room.y`) and the platforms and steps within a room (`levels`); the texture under a floor says wood or tile. Room names come from the scan's own room labels. Nothing needs a model, but a floor-plan picture added alongside (read by the `LLM_*` model as above) fills in what the scan cannot see: rooms nobody walked into, doors that were shut, rooms that end at a full-height wardrobe, window sizes, and north. Every such fill-in is listed in the review.

用这个户型 makes it the home and keeps the scan as `data/scan/homes/<home id>/model.glb`; 实景 in the top bar then shows it in place of the drawn walls, cut at the same height. The link is used to download and is never stored or logged. Tests run on a made-up flat built in the test code (no network); a real scan saved in `server/internal/scan/testdata/private/` (kept out of git) is tested too when it is there; `SCAN_LIVE_URL_FILE=<file with a link> go test ./internal/scan -run Live` fetches a real one, and `SCAN_DEBUG=<dir>` writes the plan, the model and a view from above there. `kill -HUP` restarts the server in place, after a rebuild say.

## Finding models by a sentence

家具库 → 说一句 takes a sentence such as 卧室A 放一个床头柜 or 客厅加一盆大绿植. The LLM (when set) turns it into English keywords and a room; without one, common kinds of furniture and the home's room names are matched directly. The keywords are searched in Polyfork's catalogue (`https://polyfork.dev/api/assets`, free models only), and the one picked is downloaded once to `data/catalog/<id>.glb`, put in the 家具库 and placed in that room at its real size. The 家庭助手 can do the same with its `find_models` tool. These models are CC BY 4.0; each keeps its credit line in the product (`credit`). Tests use a fake catalogue: `go test ./internal/catalog`.

## A test Home Assistant

Any Home Assistant will do; one with the `demo` integration gives lights, climate and covers to map without real devices. A Core install in a uv venv listening on http://127.0.0.1:38123 is enough. Since HA 2026.10 the port and bind address are kept in `config/.storage/http`, not in `configuration.yaml`. Before HomeTwin can connect, someone has to finish HA's onboarding in the browser and create a long-lived access token (Profile → Security), then store that token in the Keychain with the command above.

## Third-party models

The furniture and the fixtures (sofa, bed, tables, chairs, wardrobe, toilet, basin, shower, kitchen cabinets, fridge and so on) are from the Polyfork Open Plan kit (https://polyfork.dev), licensed under CC BY 4.0. They are in `web/public/models/polyfork/`, renamed by kind, with each model's credit line in `CREDITS.txt` there. Each is stretched to the real size of the piece it stands for; kinds the kit has no model for are still built in the browser.

The earlier fridge (`web/public/models/simready/fridge.glb`, no longer shown) is converted from NVIDIA SimReady `appliance_refrigerator_a01` (decimated and recoloured), licensed under CC-BY-4.0. The license text is next to it.

## License

The code is MIT (see `LICENSE`). The models under `web/public/models/` keep their own licenses (CC BY 4.0), listed next to them.
