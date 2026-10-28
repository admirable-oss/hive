{
  description = "Hive: coding agents that keep working when you close the terminal";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";

  outputs = { self, nixpkgs }:
    let
      systems = [ "x86_64-linux" "aarch64-linux" "x86_64-darwin" "aarch64-darwin" ];
      forAll = f: nixpkgs.lib.genAttrs systems (system: f nixpkgs.legacyPackages.${system});
    in
    {
      packages = forAll (pkgs: rec {
        hive = pkgs.buildGoModule {
          pname = "hive";
          version = self.shortRev or self.dirtyShortRev or "dev";
          src = self;
          # The hash of `go mod vendor`; after changing go.mod, update it with
          # `go run ./tools/nixhash` (a test fails until it matches).
          vendorHash = "sha256-Tdi6MVHCfawaKAaZgvNO+fAVJ5jcW56ntXRcmaVsTII=";
          subPackages = [ "cmd/hive" ];
          env.CGO_ENABLED = 0;
          ldflags = [
            "-s"
            "-w"
            "-X github.com/admirable-oss/hive/internal/buildinfo.commit=${self.rev or self.dirtyRev or ""}"
          ];
          # The tests drive real terminals and daemons; CI runs them.
          doCheck = false;
          meta = {
            description = "Coding agents that keep working when you close the terminal";
            homepage = "https://hive.admir-saheta.com";
            license = pkgs.lib.licenses.mit;
            mainProgram = "hive";
            platforms = pkgs.lib.platforms.unix;
          };
        };
        default = hive;
      });

      apps = forAll (pkgs: {
        default = {
          type = "app";
          program = "${self.packages.${pkgs.stdenv.hostPlatform.system}.hive}/bin/hive";
        };
      });
    };
}
