# Key bindings

<!-- Generated from internal/tui/keymap: go test ./internal/tui/keymap -update. Do not edit by hand. -->

Hive's UI is modal, like tmux. In **terminal mode** keys go to the focused
pane. The **prefix** key (`ctrl+b` unless configured) arms **prefix mode**
for one key: `ctrl+b` then `%` splits the pane, for example. Press the prefix
twice to send it to the pane. `ctrl+b ?` lists every binding in the UI, with a
filter.

Every binding can be changed in the configuration file (`hive config path`);
problems are reported as warnings and the default is kept.
`hive config reset-keys` puts the defaults back.

```toml
[keys]
prefix_keys = ["ctrl+b", "ctrl+a"]   # several prefixes are allowed

[keys.prefix]
split_right = ["|", "%"]             # replace an action's keys
zoom = []                            # unbind it

[keys.terminal]
focus_left = "alt+h"                 # works without the prefix
```

Actions are written with underscores in the file (`split_right`) and keys as
`ctrl+x`, `alt+x`, `shift+tab`, `C-x`, `M-x`, a character (`%`, `A`) or a name
(`enter`, `esc`, `space`, `tab`, `up`, `pgdown`, `f5`).

## Mouse

- Click a pane to focus it, a tab to show it, `+` for a new tab, a sidebar row to
  switch environment or go to an agent. A tab marked • has output you have
  not seen.
- Drag a border between panes, or the sidebar's edge, to resize.
- Drag over text to copy it; double-click copies a word. Ctrl-click opens a
  link: an OSC 8 hyperlink, or a URL in the text. Hyperlinks also reach your
  terminal, so its own link handling (cmd-click, say) works too.
- The wheel scrolls into the pane's history (copy mode); scrolling back to the
  bottom leaves it.
- Programs that use the mouse themselves (vim, htop) get it; hold shift to
  select with the outer terminal instead.

## Prefix mode (after `ctrl+b`)

One key after the prefix, then back to terminal mode.

| Keys | Action | |
|---|---|---|
| `x` | `close_pane` | close the pane (asks first) |
| `&` | `close_tab` | close the tab (asks first) |
| `d` | `detach` | leave the UI (agents keep running) |
| `e` | `edit_scrollback` | open the pane's scrollback in $EDITOR |
| `alt+1` | `focus_agent_1` | agent 1 in the sidebar |
| `alt+2` | `focus_agent_2` | agent 2 in the sidebar |
| `alt+3` | `focus_agent_3` | agent 3 in the sidebar |
| `alt+4` | `focus_agent_4` | agent 4 in the sidebar |
| `alt+5` | `focus_agent_5` | agent 5 in the sidebar |
| `alt+6` | `focus_agent_6` | agent 6 in the sidebar |
| `alt+7` | `focus_agent_7` | agent 7 in the sidebar |
| `alt+8` | `focus_agent_8` | agent 8 in the sidebar |
| `alt+9` | `focus_agent_9` | agent 9 in the sidebar |
| `j` `down` | `focus_down` | focus the pane below |
| `h` `left` | `focus_left` | focus the pane to the left |
| `o` | `focus_next` | focus the next pane |
| `;` | `focus_prev` | focus the previous pane |
| `l` `right` | `focus_right` | focus the pane to the right |
| `k` `up` | `focus_up` | focus the pane above |
| `w` `s` | `goto` | go to an agent or pane (fuzzy) |
| `?` | `help` | show key bindings |
| `[` `v` | `mode_copy` | copy mode |
| `space` | `mode_navigate` | navigate mode (sticky) |
| `r` | `mode_resize` | resize mode (sticky) |
| `esc` | `mode_terminal` | back to terminal mode |
| `c` | `new_tab` | new tab |
| `a` | `next_agent` | go to the next agent that is blocked or done |
| `)` | `next_env` | next environment |
| `n` | `next_tab` | next tab |
| `N` | `open_notification_target` | go to the agent of the latest notification |
| `O` | `overview` | Overview dashboard (toggle) |
| `]` | `paste` | paste the last copied text |
| `f` | `popup` | open a shell in a popup |
| `(` | `prev_env` | previous environment |
| `p` | `prev_tab` | previous tab |
| `.` | `rename_pane` | rename the pane |
| `,` | `rename_tab` | rename the tab |
| `J` `alt+down` | `resize_down` | move the bottom border |
| `H` `alt+left` | `resize_left` | move the left border |
| `L` `alt+right` | `resize_right` | move the right border |
| `K` `alt+up` | `resize_up` | move the top border |
| `ctrl+b` | `send_prefix` | send the prefix key to the pane |
| `"` `-` | `split_down` | split the pane, new pane below |
| `%` `\|` | `split_right` | split the pane, new pane on the right |
| `}` | `swap_next` | swap the pane with the next one |
| `{` | `swap_prev` | swap the pane with the previous one |
| `1` | `tab_1` | tab 1 |
| `2` | `tab_2` | tab 2 |
| `3` | `tab_3` | tab 3 |
| `4` | `tab_4` | tab 4 |
| `5` | `tab_5` | tab 5 |
| `6` | `tab_6` | tab 6 |
| `7` | `tab_7` | tab 7 |
| `8` | `tab_8` | tab 8 |
| `9` | `tab_9` | tab 9 |
| `b` | `toggle_sidebar` | show or hide the sidebar |
| `z` | `zoom` | zoom the pane to fill the tab (toggle) |

## Navigate mode

Sticky: keys move between panes and tabs until `esc`.

| Keys | Action | |
|---|---|---|
| `x` | `close_pane` | close the pane (asks first) |
| `j` `down` | `focus_down` | focus the pane below |
| `h` `left` | `focus_left` | focus the pane to the left |
| `o` | `focus_next` | focus the next pane |
| `l` `right` | `focus_right` | focus the pane to the right |
| `k` `up` | `focus_up` | focus the pane above |
| `w` `/` | `goto` | go to an agent or pane (fuzzy) |
| `[` | `mode_copy` | copy mode |
| `r` | `mode_resize` | resize mode (sticky) |
| `esc` `q` `enter` `i` | `mode_terminal` | back to terminal mode |
| `c` | `new_tab` | new tab |
| `a` | `next_agent` | go to the next agent that is blocked or done |
| `)` | `next_env` | next environment |
| `n` `tab` | `next_tab` | next tab |
| `(` | `prev_env` | previous environment |
| `p` `shift+tab` | `prev_tab` | previous tab |
| `"` `-` | `split_down` | split the pane, new pane below |
| `%` `\|` | `split_right` | split the pane, new pane on the right |
| `}` | `swap_next` | swap the pane with the next one |
| `{` | `swap_prev` | swap the pane with the previous one |
| `1` | `tab_1` | tab 1 |
| `2` | `tab_2` | tab 2 |
| `3` | `tab_3` | tab 3 |
| `4` | `tab_4` | tab 4 |
| `5` | `tab_5` | tab 5 |
| `6` | `tab_6` | tab 6 |
| `7` | `tab_7` | tab 7 |
| `8` | `tab_8` | tab 8 |
| `9` | `tab_9` | tab 9 |
| `z` | `zoom` | zoom the pane to fill the tab (toggle) |

## Resize mode

Sticky: keys move the focused pane's borders until `esc`.

| Keys | Action | |
|---|---|---|
| `space` | `mode_navigate` | navigate mode (sticky) |
| `esc` `q` `enter` `i` | `mode_terminal` | back to terminal mode |
| `j` `down` | `resize_down` | move the bottom border |
| `h` `left` | `resize_left` | move the left border |
| `l` `right` | `resize_right` | move the right border |
| `k` `up` | `resize_up` | move the top border |
| `z` | `zoom` | zoom the pane to fill the tab (toggle) |

## Copy mode

Vim keys over the pane's history and screen. Search is case-insensitive unless the query has a capital letter.

| Keys | Action | |
|---|---|---|
| `G` | `copy_bottom` | bottom |
| `j` `down` | `copy_down` | down |
| `q` `esc` | `copy_exit` | leave copy mode |
| `^` | `copy_first_nonblank` | first non-blank |
| `ctrl+d` | `copy_half_down` | half a page down |
| `ctrl+u` | `copy_half_up` | half a page up |
| `h` `left` | `copy_left` | left |
| `$` `end` | `copy_line_end` | end of line |
| `0` `home` | `copy_line_start` | start of line |
| `ctrl+f` `pgdown` | `copy_page_down` | page down |
| `ctrl+b` `pgup` | `copy_page_up` | page up |
| `l` `right` | `copy_right` | right |
| `L` | `copy_screen_bottom` | bottom of screen |
| `M` | `copy_screen_middle` | middle of screen |
| `H` | `copy_screen_top` | top of screen |
| `?` | `copy_search_backward` | search up |
| `/` | `copy_search_forward` | search down |
| `n` | `copy_search_next` | next match |
| `N` | `copy_search_prev` | previous match |
| `v` `space` | `copy_select` | select (toggle) |
| `V` | `copy_select_line` | select lines (toggle) |
| `g` | `copy_top` | top of history |
| `k` `up` | `copy_up` | up |
| `e` | `copy_word_end` | end of word |
| `w` | `copy_word_next` | next word |
| `b` | `copy_word_prev` | previous word |
| `y` `enter` | `copy_yank` | copy the selection and leave |
