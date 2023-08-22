{ config, pkgs, ... }:
let
  cfg = config.services.watchinator;
in
{
  options = {
    services.watchinator = {
      package = pkgs.lib.mkOption {
        defaultText = "pkgs.watchinator";
        type = pkgs.lib.types.package;
        description = "the watchinator package";
      };
      enable = pkgs.lib.mkOption {
        default = false;
        type = pkgs.lib.types.bool;
        description = "start the watchinator service";
      };
      config = pkgs.lib.mkOption {
        default = "";
        type = pkgs.lib.types.str;
        description = "configuration file content";
      };
    };
  };
  config = pkgs.lib.mkIf cfg.enable {
    users.users.watchinator = {
      isSystemUser = true;
      group = "watchinator";
    };
    users.groups.watchinator = {};
    system.activationScripts = {
      watchinator-etc = pkgs.lib.stringAfter [ "etc" ] ''
        mkdir -p /etc/watchinator
        chown -R watchinator:watchinator /etc/watchinator
        chmod -R 0755 /etc/watchinator
      '';
      watchinator-config = pkgs.lib.stringAfter [ "watchinator-etc" ] ''
        [ -f "/etc/watchinator/config.yaml" ] && mv /etc/watchinator/config.yaml /etc/watchinator/config.yaml.bak
        touch /etc/watchinator/config.yaml
        chown watchinator:watchinator /etc/watchinator/config.yaml
        chmod 0400 /etc/watchinator/config.yaml
        echo '${cfg.config}' >> /etc/watchinator/config.yaml
      '';
    };
    systemd.services.watchinator = {
      description = "Run watchinator";
      serviceConfig = {
        Type = "simple";
        ExecStart = "${cfg.package}/bin/watchinator watch --config /etc/watchinator/config.yaml";
      };
    };
  };
}
