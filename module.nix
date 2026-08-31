{ self }:
{ config, lib, pkgs, ... }:
let
  cfg = config.services.watchinator;

  inherit (pkgs.stdenv.hostPlatform) isDarwin isLinux;

  # Must agree with the xdg.configFile target below. Neither systemd nor launchd
  # expands `~`.
  configPath = "${config.xdg.configHome}/watchinator/config.yaml";

  argv = [ "${cfg.package}/bin/watchinator" "watch" "--config" configPath ] ++ cfg.extraArgs;

  # Exec* lines are not shell: quote with "..." and neutralize % specifiers and $
  # expansion. Mirrors nixpkgs' escapeSystemdExecArg (nixos/lib/utils.nix), which
  # lives in NixOS's utils rather than lib, so it is unreachable from here.
  escapeExecArg = arg: builtins.replaceStrings [ "%" "$" ] [ "%%" "$$" ] (builtins.toJSON arg);
  execStart = lib.concatMapStringsSep " " escapeExecArg argv;
in
{
  # Fail loudly if this gets imported as a NixOS or nix-darwin module.
  _class = "homeManager";

  options.services.watchinator = {
    enable = lib.mkEnableOption "watchinator";
    package = lib.mkOption {
      type = lib.types.package;
      default = self.packages.${pkgs.stdenv.hostPlatform.system}.watchinator;
      defaultText = lib.literalExpression "watchinator.packages.\${system}.watchinator";
      description = "the watchinator package";
    };
    config = lib.mkOption {
      default = "";
      type = lib.types.str;
      description = "configuration file content";
    };
    extraArgs = lib.mkOption {
      default = [ ];
      type = lib.types.listOf lib.types.str;
      example = [ "--verbose" "--log-json" ];
      description = "extra args to pass to watchinator";
    };
    logFile = lib.mkOption {
      type = lib.types.str;
      default = "${config.home.homeDirectory}/Library/Logs/watchinator.log";
      defaultText = lib.literalExpression ''"''${config.home.homeDirectory}/Library/Logs/watchinator.log"'';
      description = ''
        Where launchd writes the service's stdout and stderr. Darwin only: on
        Linux the journal already captures both, and there is nothing to point
        at a file.
      '';
    };
  };

  config = lib.mkIf cfg.enable (lib.mkMerge [
    {
      # home-manager silently drops `systemd.user` on darwin and `launchd.agents`
      # on Linux (each module gates its whole config block on a platform-defaulted
      # `enable`), so an unsupported platform would install the config file and no
      # service at all, with no error. Refuse instead.
      assertions = [
        {
          assertion = isLinux || isDarwin;
          message = "services.watchinator: no service backend for ${pkgs.stdenv.hostPlatform.system}; only Linux (systemd) and Darwin (launchd) are supported.";
        }
      ];

      xdg.configFile."watchinator/config.yaml".text = cfg.config;
    }

    (lib.mkIf isLinux {
      systemd.user.services.watchinator = {
        Unit = {
          Description = "Subscribe to things on GitHub using custom filters";
          After = "network.target";
        };
        Service = {
          Type = "simple";
          ExecStart = execStart;
          # A poller outlives transient network and GitHub API failures; without
          # this a single error exit leaves it dead until the next login.
          Restart = "on-failure";
          RestartSec = 60;
        };
        Install = {
          WantedBy = [ "default.target" ];
        };
      };
    })

    (lib.mkIf isDarwin {
      launchd.agents.watchinator = {
        enable = true;
        config = {
          # home-manager rewrites this into a /bin/wait4path wrapper so the agent
          # does not start before the Nix store is mounted.
          ProgramArguments = argv;
          RunAtLoad = true;
          # launchd's analogue of Restart=on-failure. A bare `KeepAlive = true`
          # would also resurrect clean exits, which is not what the Linux unit does.
          KeepAlive = {
            SuccessfulExit = false;
          };
          # Back off between respawns; launchd's default of 10s hammers the API.
          ThrottleInterval = 60;
          StandardOutPath = cfg.logFile;
          StandardErrorPath = cfg.logFile;
        };
      };
    })
  ]);
}
