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
    version = "main";
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
          description = "Subscribe to things on GitHub using custom filters";
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
