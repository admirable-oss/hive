# Hive on your phone

Your agents don't stop when you leave your desk. The [web observer](web.md) is
built for phones too:
- it shows what needs you first;
- it notifies you when an agent is blocked or done;
- a tap on a notification takes you straight to that agent.

It stays read-only: nothing on a phone can type into an agent, change a layout
or start anything.

## Set up

1. **Serve the web observer over HTTPS** where your phone can reach it. See
   [deployment](web.md#deployment): for example Tailscale with
   `tailscale cert`, or a reverse proxy. Phones need HTTPS for notifications
   and for installing the app. Plain HTTP works only on `localhost`.
2. **Share a link** to your phone, for example as a QR code from any QR tool:
   ```sh
   hive web share --runtime laptop --screens --uses 2 --expires 720h --label "my phone"
   ```
   `--uses 2` lets the same link sign in both the browser and the installed
   app. On iOS these sign in separately.
3. **Install it.**
   - **iPhone, iPad:** open the link in Safari, then Share → **Add to Home
     Screen**. Open Hive from the Home Screen and sign in there: paste the
     code (the part after `#` in the link) on the sign-in page.
   - **Android:** open the link in Chrome, then ⋮ → **Install app**, or accept
     the prompt.
4. **Turn notifications on.** In the app, Settings → **Turn on notifications**,
   and allow them when asked. **Send a test** checks the whole path.

## What you see

- **Attention**, the first page of the app, shows agents that are blocked or
  done, newest first.
  - The *Working* and *All* chips widen the list.
  - Agents you **Follow** are pinned at the top.
- **Cards** show each agent's state (colour and words), what it is, where it
  runs, since when, and why. The whole card is the link.
- **An agent's page** shows its state, why, and its activity. With screen
  access it also shows the screen, live:
  - The screen scrolls sideways inside its own box.
  - **A−/A+** change the text size, **Fit** sizes it to your screen's width,
    and **Wrap** wraps long lines.
  - **Auto-scroll** keeps the newest line in view.
  - There is no keyboard and no input. Tapping the screen does nothing to the
    agent.
- **Output history** appears only if the link allows it (`--history`). It
  loads only when you press the button, and every read is audited.
- **Machines** lists each runtime, its connection and its environments.
- **Navigation.** The bottom tab bar reaches every section. **‹ Back** returns
  to where you came from, with the list where you left it.
- **When something isn't current, it says so.** A banner names any runtime
  that is unreachable and shows the time of its last data. Its screens say
  "As of 12:01: not updating", and the header shows Reconnecting, Offline or
  stale.
- **With no network,** the installed app shows an offline page. It never
  shows data it kept, because it keeps none.
- **Accessibility.**
  - Text follows your system size (Dynamic Type on iOS, font size on
    Android).
  - Every control is at least 44 points tall.
  - States are words as well as colours.
  - Screen readers hear live connection changes.

## Notifications

You can be notified when an agent:
- is **blocked**, waiting for your decision (on by default);
- is **done**, having finished a task (on by default);
- **stopped with an error** (off by default).

Settings also chooses which runtimes notify you, and how much a notification
says. To silence one agent, **Mute** it from its page for an hour, until
tomorrow, or until unmuted. Mutes are listed in Settings.

**What a notification contains**
- By default: the agent's name, kind, environment and machine, e.g.
  "dev-agent is waiting for you · claude in api on laptop", plus a link to
  that agent.
- With **"nothing but An agent needs you"**: no names at all, and the link
  goes to Attention.
- **Never** what is on a screen, output, or anything an agent printed.

**Privacy and security**
- **Encrypted end to end.** The message is encrypted for your device (Web
  Push, RFC 8291). The push service that carries it (Apple, Google, Mozilla,
  Microsoft) cannot read it.
- **No relay.** Your web observer sends it directly, signed with its own key
  (VAPID). No Hive service sits in between.
- **Lock screen.** Whether notifications show there, and with what detail, is
  up to your phone's settings. Hive does not override them.

**Each change notifies once**
- An agent that blocks notifies once, however often the screen is redrawn or
  the connection drops.
- A change missed while the web observer was disconnected is told once when
  it catches up.
- A burst of changes is limited to 6 notifications a minute per device; the
  rest are summed up in one "N more updates".
- Repeats for the same agent replace each other on the device, and in the
  push service while undelivered.

**Access ends notifications**
- Revoking a link, its expiry, or signing out removes its devices: nothing
  more is sent.
- Each notification is checked against the link's access just before it is
  sent, so an agent outside the link's scope never notifies.

**Delivery is best effort**
- The web observer must be running. It stays connected to runtimes that have
  subscribed devices, so the multiplexer shows `◉ observed by hive-web:…`
  while notifications are on.
- **iOS and iPadOS:** 16.4 or newer, and only from the app added to the Home
  Screen (not Safari tabs). Focus modes and Scheduled Summary can hold
  notifications back.
- **Android:** Chrome, Firefox, Samsung Internet and Edge. Battery
  optimisation (Doze) can delay notifications while the phone sleeps. They
  are sent "urgent" for blocked agents, which shortens the delay.
- **Desktop browsers** work too, in a tab or installed.
- A notification undelivered after 4 hours (blocked) or 1 hour (done) is
  dropped. By then Attention shows the current state anyway.
- `hive web push status` lists the devices, their last delivery and any
  errors. `hive web push test <grant-or-session>` sends a test.

## Configuration

```toml
[web]
push = true                    # notifications (Web Push)
push_contact = ""              # mailto: or https: contact for push services ("" uses the public URL)
push_allow_any_endpoint = false
```

Devices may only use the browsers' own push services: Google, Mozilla, Apple
and Microsoft. That way the web observer never posts to arbitrary addresses.
`push_allow_any_endpoint` lifts this limit for self-hosted push services.

## Why an installable web app, and not native apps (yet)

The plan for M16 asked whether to build native apps (Swift for iOS, Kotlin
for Android) or an installable web app (PWA). We chose the PWA, built on the
same Go and gomponents web observer.

- **Push decides it.** A native app can only receive notifications that the
  app's publisher sends through Apple's or Google's services, with the
  publisher's credentials. A self-hosted Hive would have to route every
  notification through a relay run by the Hive project, which would see your
  devices and traffic. The alternative is every user holding an Apple
  developer account.
- **Web Push needs neither.** Each web observer has its own key, sends
  straight to the browser's push service, and the payload is encrypted for
  the device.
- **One code base,** tested in CI with real browsers at phone sizes. The same
  observer API (`api/v1/observer.txt`) would back any future client.
- **What native would add:** Live Activities, widgets, background refresh
  without push, and use on managed devices that cannot install web apps.
  These are the triggers to revisit. A native app would then reuse the
  observer API and this notification design, with a relay that the user
  runs.

## Supported devices and how they are tested

| | Supported | Tested |
|---|---|---|
| iPhone, iPad | iOS/iPadOS 16.4+, Safari and the Home Screen app | Automated: iPhone 13 emulation in Chrome (layout, touch targets, deep links). Manual: the checklist below |
| Android | Chrome, Firefox, Samsung Internet, Edge (current) | Automated: Pixel 5 emulation. Manual: the checklist below |
| Screens | Phone widths and up, portrait and landscape | At 390 and 393 px (iPhone 13, Pixel 5): no sideways page scrolling; screens scroll inside their box |

The automated tests (`internal/web/*_test.go`) cover:
- **Phone layout:** no sideways scrolling, 44-point targets, tab bar, screen
  scrolling, Fit, Back.
- **The app shell:** the manifest, the service worker caching only the shell,
  and the offline page.
- **Deep links:** a notification's link going through sign-in and back.
- **Notifications:** sent to a fake push service that decrypts them with the
  device's key and checks the server's signature:
  - once per transition, scope and mutes honoured;
  - private detail;
  - no screen contents;
  - removed after revocation, and a gone device dropped.
- **Expiry and reconnects:** expired and revoked links, and reconnecting
  after the runtime restarts.

Before a release, on real devices:
1. Install from Safari (iOS) and from Chrome (Android), and sign in in the
   app.
2. Turn notifications on, and send a test.
3. Make an agent block and finish. Check one notification each, and that
   tapping opens the agent.
4. Lock the phone. Check the notification follows the lock-screen preview
   setting.
5. Revoke the link (`hive web revoke`). The app goes to sign-in, and no more
   notifications arrive.
6. Turn on airplane mode, then open the app: the offline page shows. Turn it
   off: the app catches up.
