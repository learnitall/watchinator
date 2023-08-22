{
  description = "watchinator";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-23.05";
    flake-utils = {
      url = "github:numtide/flake-utils";
    };
  };

  outputs = { self, nixpkgs, flake-utils }: 
  let
    version = "0.1";
    tag = version;
    commit = if (self ? rev) then self.rev else "dirty";
  in
  flake-utils.lib.eachDefaultSystem (system: 
    let
      pkgs = nixpkgs.legacyPackages.${system};
      
      goDrv = pkgs.buildGoModule {
        pname = "watchinator";
        inherit version;
        inherit tag;
        inherit commit;

        src = self;

        CGO_ENABLED = false;
        ldflags = [
          "-X github.com/learnitall/watchinator/cmd.commit=${commit}"
          "-X github.com/learnitall/watchinator/cmd.tag=${tag}"
        ];
        vendorHash = "sha256-yqZJV8bTGHMAb699CjAbQIx1n/YjK+7nvE0hHdIPsbs=";

        buildInputs = with pkgs; [
          golangci-lint
        ];

        preBuild = ''
          export HOME=$(pwd)
          ${pkgs.golangci-lint}/bin/golangci-lint run --config .golangci-lint.yaml \
            --verbose
        '';

        meta = {
          description = "";
          homepage = "https://github.com/learnitall/watchinator";
          license = pkgs.lib.licenses.mit;
          maintainers = [
            {
              name = "Ryan Drew";
              email = "learnitall0@gmail.com";
              github = "learnitall";
            }
          ];
        };
      };

      dockerImage = pkgs.dockerTools.buildImage {
        name = "watchinator";
        inherit tag;

        fromImage = pkgs.dockerTools.pullImage {
          imageName = "cgr.dev/chainguard/go";
          imageDigest = "sha256:5a478f52d08abb5a9bbd9acae52e9ff89185f7bfd420f7ecea12d63810192452";
          sha256 = "sha256-6ozuLvUVNz1N+Bre5fq6XL+d+8LsgZPKNLPVRFvE9mA=";
          finalImageTag = "1.20";
        };

        copyToRoot = pkgs.buildEnv {
          name = "image-root";
          paths = [ 
            goDrv
            pkgs.fakeNss
          ];
          pathsToLink = [
            "/bin"
            "/etc"
            "/var"
          ];
        };

        config = {
          Entrypoint = [ "/bin/watchinator" ];
          User = "nobody:nobody";
          WorkingDir = "/opt/watchinator";
        };
      };
    in {
      packages = rec {
        watchinator-image = dockerImage;
        watchinator = goDrv;
        default = watchinator;
      };
      apps = rec {
        watchinator = flake-utils.lib.mkApp {
          drv = self.packages.${system}.watchinator;
        };
        default = watchinator;
      };
      nixosModules = rec {
        watchinator = {
          imports = [ ./module.nix ];
        };
        default = watchinator;
      };
    }
  );
}
