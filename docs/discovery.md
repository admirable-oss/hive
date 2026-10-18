# Finding machines nearby

Hive can find other machines running it on your local network. Finding a
machine never trusts it, though. You pair with it on purpose: Hive checks who it
is over SSH and asks you first. Only after that does it save the machine.

```sh
hive machine discover          # who is announcing themselves nearby
hive machine pair studio       # check its identity, confirm, save it
hive --machine studio agent list
```

Saved machines work as before ([remote machines](https://hive.admir-saheta.com/docs/use/rc-ra/)):
`--machine`, the sidebar, `hive machine status`. Discovery only adds a way to
find them and to know them by identity.

## Turn it on

A machine announces itself only when its config says so:

```toml
[discovery]
advertise = true
name = "studio"      # "" is the host name
ssh_port = 22        # where ssh listens there
```

Its daemon then announces it while it runs. The daemon says goodbye when it
stops, so the machine drops out of other machines' lists.

On the machine you look from, nothing needs turning on: `hive machine discover`
asks the network when you run it. Set `browse = false` to turn that off. Saved
machines and `pair --host` keep working.

## Find machines

```text
$ hive machine discover
NAME     ID         ADDRESS           STATE
laptop   hv7k1d2a   192.168.1.9       trusted (saved as "laptop")
studio   q3xdk2ab   192.168.1.7 +1    new: hive machine pair studio
```

- **ID** is the start of the machine's identity (see below). An ID you have not
  paired with is only a claim.
- **STATE** is `new` for runtimes you have not saved. Saved ones show their
  state (see [machine states](#machine-states)).
- Two runtimes with one name are both listed and marked. A runtime that takes a
  name already in use is renamed: "studio (2)".
- `hive machine list --discover` shows saved machines and runtimes nearby
  together.

## Pair

```text
$ hive machine pair studio
hive: machine "studio"
  address:     192.168.1.7
  identity:    q3xdk2abfj4ldtxk5nqbbmvy7a
  fingerprint: SHA256:Mzt1xYZ0UlTMbYrf0m/aNPzuAlugEHX5hlrWUOm12Dc
  name:        studio (host studio.lan)
  platform:    linux/amd64, hive 1.0.8
  trust:       new: not paired before
hive: trust this machine and save it as "studio"? [y/N]
```

Pairing does the following:

1. It connects over SSH, with your keys and `~/.ssh/config`. ssh checks the
   host key as usual. For discovered machines it files that key under the
   machine's identity (`HostKeyAlias`), not its address.
2. It asks Hive there to sign a fresh challenge with its identity key.
3. It checks the signature, and that the identity is the one announced. If it is
   not, pairing stops: someone else may have sent the announcement.
4. It shows the result and asks you. To compare fingerprints out of band, run
   `hive machine identity` on that machine.

Options:
- `--as <name>` saves it under another name.
- `--user <user>` sets the ssh user.
- `--yes` skips the question (for scripts).
- `<name>` can also be the start of the machine's ID, when two machines share a
  name.

## Identity and trust

Every Hive installation has an identity: an ed25519 key pair. Hive makes it on
first use and keeps it in `~/.hive/identity.json`, readable by you alone. The
identity does not change when the machine's address or name changes. It does
change when hive's home there is removed, or when another machine answers at
that address.

Hive checks it again in these cases:
- `hive machine status` checks every machine it reaches and reports
  `verified`, `changed`, or `unpinned` (a machine saved with `hive machine add`,
  which has no identity pinned).
- `hive machine trust <name>` checks one machine. On a machine without an
  identity, it pins the one it finds (after asking).
- `hive machine discover` follows a paired machine to a new address, after it
  proves its identity there. An announcement alone never moves a machine.

**A changed identity stops Hive from using the machine.**
- `--machine` refuses it.
- The sidebar leaves it out.

`hive machine trust <name>` shows the old identity and the new one, and asks you.
Accept only if you know why the identity changed, for example because you
reinstalled hive there.

`hive machine revoke <name>` forgets a machine's identity and disables the
machine. To use it again, run `hive machine trust` and then
`hive machine enable`. `hive machine remove` forgets the machine entirely.

## Machine states

| State | Meaning |
|---|---|
| `trusted` | Saved, with its identity pinned (paired or trusted). |
| `configured` | Saved without an identity (`hive machine add`). ssh alone says who it is. |
| `changed` | It proved an identity other than the pinned one. Hive does not use it until you run `trust`. |
| `disabled` | Saved, but Hive does not connect to it (`disable`, `revoke`). |
| `discovered` | Found nearby, not saved. |

`hive machine status` adds whether each machine is reachable now (connected or
disconnected), along with its round trip, version and agents.

## Without multicast

Discovery uses multicast DNS (mDNS, UDP port 5353). Multicast does not reach
everywhere:
- across subnets and routers;
- on guest Wi-Fi with client isolation;
- across most VPNs;
- through firewalls that drop mDNS.

There are two other ways:
- **Ask a machine directly:**
  `hive machine discover --address 10.8.0.12` (or `pair <name> --address …`)
  sends the same question straight to that machine. It works over a VPN or across
  subnets, as long as UDP 5353 gets through.
- **Pair by ssh:** `hive machine pair --host me@10.8.0.12` skips discovery and
  pins the identity in the same way.

## Network behaviour

- **Interfaces.**
  - By default, Hive announces and looks on every interface that is up and
    multicast-capable.
  - It skips loopback, point-to-point links (most VPN tunnels) and links that
    have only link-local addresses (such as Apple's peer-to-peer Wi-Fi).
  - Set `interfaces = ["en0"]`, or pass `--interface`, to choose interfaces. A
    VPN interface you name is used.
- **Addresses.** A machine answers with the addresses of the interface the
  question arrived on, not those of every interface.
- **Traffic.**
  - A daemon checks that its name is free (three probes), then announces itself
    twice and stays quiet. It answers the same question on an interface at most
    once a second, and answers at most 20 direct questions a second.
  - `discover` sends three questions, one second apart, and listens for three
    seconds (`--timeout`).
- **Several sessions.** Every session on a machine shares one identity. Only one
  daemon announces the machine.

When nothing is found, `discover` prints:
- the interfaces it asked on;
- the interfaces it skipped, and why;
- the addresses it asked directly;
- what may be in the way.

`--json` includes the same details.

## What is announced, and what is not

An announcement contains only:
- the discovery name;
- the identity ID;
- the ssh port;
- Hive's API level;
- the capability `ssh`;
- the addresses of the interface the question arrived on, under a host name made
  from the ID (`hive-q3xdk2ab.local`).

It never contains:
- user names, sessions, agents, environments or paths;
- screen contents;
- credentials or tokens.

The proof that pairing asks for is sent only over SSH, after ssh has logged you
in. It adds the host name, platform and hive version.

**Announcing opens nothing.** The daemon answers mDNS questions on UDP 5353, and
that is all. It serves no other port and starts no control service. Everyone
still reaches Hive through your SSH server, with your accounts and keys. Turning
off `advertise` removes the announcement and nothing else.

## On untrusted networks

On a network you do not trust (cafés, conferences, shared offices), keep these
points in mind:
- **Anyone there can see the announcement.** It reveals that the machine runs
  Hive, its discovery name and its address. If that is too much, leave
  `advertise` off. To find your own machines, pair by `--host`, or use
  `--address` over a VPN.
- **Anyone can make an announcement**, with any name and any ID. Pairing defends
  against this:
  - it refuses a machine that cannot prove the ID it announced;
  - ssh checks the host key;
  - you confirm the identity.

  Compare the fingerprint when it matters.
- **A spoofed announcement cannot redirect a paired machine.** Hive moves a
  paired machine to a new address only after the machine proves its pinned
  identity there.

## Supported environments

- **macOS and Linux.** Announcing works alongside the system's mDNS responder
  (mDNSResponder, Avahi): both share port 5353. `dns-sd -B _hive._tcp` and
  `avahi-browse _hive._tcp` list Hive runtimes.
- **Windows.** It can discover and pair with other machines. It does not
  announce itself, because Windows is not yet supported as a remote machine
  ([platforms](platforms.md)): a daemon with `advertise = true` logs that and
  does not announce. Windows Firewall must let the answers in (UDP).
- **Containers and WSL.** The default network modes (NAT) do not pass
  multicast. Use host networking, or use `--address` or `--host`.

## Configuration

| Key | Default | |
|---|---|---|
| `discovery.advertise` | `false` | Announce this machine while the daemon runs |
| `discovery.name` | `""` | The name to announce (`""`: the host name) |
| `discovery.ssh_port` | `22` | The ssh port the announcement gives |
| `discovery.interfaces` | `[]` | Interfaces to announce and look on (`[]`: automatic) |
| `discovery.browse` | `true` | Let `hive machine discover` look |

`HIVE_DISCOVERY_ADDR=host:port` makes a daemon answer direct questions on that
address only, without multicast. It is meant for tests and unusual networks.
