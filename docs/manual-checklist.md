# Manual checklist

For the parts automated tests cannot see. Run after changes to `internal/gui`, `internal/notify` or `internal/daemon`.

- [ ] `focus start "check" 1m`, close every terminal. After a minute a notification appears and the sound plays.
- [ ] The popup opens by itself with "Time's up", the goal, and the reflection box focused.
- [ ] Typing a reflection and pressing Save shows "Saved" and the popup closes after about 1.5s.
- [ ] `focus log` shows the reflection.
- [ ] With the break box checked, `focus status` shows a break. When it ends, a "Break over" notification appears.
- [ ] `focus gui` while a session runs shows the countdown. Running `focus finish` in a terminal updates the window without a reload.
- [ ] Running `focus gui` twice opens one window.
- [ ] Hyprland: the window rule from the README floats and pins the window at the configured spot.
- [ ] Stop the daemon (`systemctl --user stop focusd`) with the window open. It shows "Can't reach the daemon", and recovers after `systemctl --user start focusd`.
