# starter-upper

A terminal browser for everything that starts automatically on macOS. It flags leftovers and risky items, and can disable or delete them.

```
go install github.com/masto/starter-upper@latest
starter-upper              # interactive browser
starter-upper report       # plain-text list of everything and its findings
```

Requires macOS and Go 1.27 or later.

## Where it looks

| Kind | Source |
|---|---|
| user agent | `~/Library/LaunchAgents` |
| global agent | `/Library/LaunchAgents` (loaded for every user) |
| daemon | `/Library/LaunchDaemons` (runs as root) |
| login item | System Settings' "Open at Login" list, read through System Events |
| app helper | `Contents/Library/LoginItems` and `Contents/Library/Launch{Agents,Daemons}` inside apps in `/Applications` and `~/Applications` |
| bg record | macOS's background-item database (`sfltool dumpbtm`) entries that match none of the above |
| sys ext | `systemextensionsctl list` |
| cron | `crontab -l` |
| startup item, login hook | `/Library/StartupItems`, loginwindow `LoginHook`/`LogoutHook` (both obsolete) |

Running state and launchd overrides come from `launchctl print` and `launchctl print-disabled`, which work without root.

The background-item database needs root. Press `b` in the browser and sudo will prompt for your password. If sudo's timestamp is still fresh, the database loads automatically at startup. You can also pass a saved dump with `--btm FILE`. Loading it adds each item's Allow-in-Background state for every user, plus other users' items.

## Checks

- **Problems:**
  - The program or `.app` an item launches no longer exists.
  - The plist is unreadable, or has no program.
  - A secret is passed on the command line in a plist every user can read.
  - A system plist is writable by group or others, or isn't owned by root.
  - The item runs from `/tmp` or `/Users/Shared`.
  - The item uses a `com.apple.` label but doesn't run Apple software.
- **Warnings:**
  - The same label is defined in more than one place.
  - A daemon runs a program that a non-root user can modify.
  - The item should be kept alive but isn't running.
  - The item runs from a hidden folder.
  - A background-item record points at a missing file.
  - The item is a StartupItem or login hook (both obsolete).
- **Notes:**
  - The item is blocked in System Settings.
  - The item is disabled but its plist is still there.
  - Nothing makes launchd start the item.
  - The plist is empty.
  - The label doesn't match the file name.
  - The item belongs to another user.

## Actions

Every action shows the exact commands it will run and waits for `y`.

- **`d` disable / re-enable:** nothing is deleted.
  - launchd items get `launchctl disable` plus `bootout`, and re-enabling uses `enable` plus `bootstrap`. For a global agent this affects only your account.
  - Login items are removed from Open at Login. They're remembered in `~/Library/Application Support/starter-upper/disabled-login-items.json`, so `d` can add them back.
  - Cron lines are commented out with a `#starter-upper-disabled:` marker.
- **`x` delete:** the file is first copied to `~/Library/Application Support/starter-upper/backups/<timestamp>/`, along with a `.restore.txt` note giving the command that puts it back.
- **Read-only items:** app helpers, background records and system extensions can only be changed by their app or by System Settings. `s` opens the Login Items & Extensions pane.

Changes to system items run `sudo -n` after the TUI is suspended to run `sudo -v`. Sudo only asks for your password when its timestamp has expired.

## License

MIT. See [LICENSE](LICENSE).
