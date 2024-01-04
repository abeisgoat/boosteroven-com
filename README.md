# boosteroven-com

Running BoosterOven.com requires the following environmental variables to be set...

```
POSTHOG_API_KEY
```

This repository is designed to exist at `~/boosteroven-com` and ran via a local `go` installation installed at `~/go`.

You may need to change "abeisgreat" to your username. Pocketbase data will be stored in `~/pb_data` which is
seperate from the `pb_data` checked into this repo which is development data and will not overwrite the production
data.

Place the following service config at `/lib/systemd/system/boosteroven.service` to enable restarting, etc.

```
[Unit]
Description = boosteroven

[Service]
Type           = simple
User           = root
Group          = root
LimitNOFILE    = 4096
Restart        = always
RestartSec     = 5s
StandardOutput = append:/home/abeisgreat/boosteroven-com/errors.log
StandardError  = append:/home/abeisgreat/boosteroven-com/errors.log
WorkingDirectory = /home/abeisgreat/boosteroven-com/
ExecStart      = /home/abeisgreat/boosteroven.com serve boosteroven.com --dir="/home/abeisgreat/pb_data"

[Install]
WantedBy = multi-user.target
```

Updating BoosterOven on a live server can be done, from `~/boosteroven-com`, with...

```
git pull && ../go/bin/go build . && mv boosteroven.com ../ && sudo systemctl restart boosteroven
```