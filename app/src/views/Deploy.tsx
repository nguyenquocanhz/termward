import { useState } from "react";
import { Copy, ExternalLink, Rocket, SquareTerminal } from "lucide-react";
import { useT, type TKey } from "../lib/i18n";
import { copyText } from "../lib/util";
import { confirmAction, navigate, openTerminal, toast, useApp } from "../store";

interface Target {
  key: string;
  name: string;
  /** Popularity badge, language-neutral (installs · stars). */
  badge: string;
  blurb: TKey;
  url: string;
  needsDomain?: boolean;
  command: (o: { domain: string }) => string;
}

// Ranked by install base (downloads), the strongest signal the user asked for.
const TARGETS: Target[] = [
  {
    key: "aapanel",
    name: "aaPanel",
    badge: "~3.6M+ · 3.1k★",
    blurb: "deploy.d.aapanel",
    url: "https://www.aapanel.com/",
    command: () =>
      `URL=https://www.aapanel.com/script/install_7.0_en.sh && (curl -ksSO "$URL" || wget --no-check-certificate -O install_7.0_en.sh "$URL") && bash install_7.0_en.sh aapanel`,
  },
  {
    key: "1panel",
    name: "1Panel",
    badge: "37k★ · OSS",
    blurb: "deploy.d.1panel",
    url: "https://1panel.pro/",
    command: () =>
      `curl -sSL https://resource.fit2cloud.com/1panel/package/v2/quick_start.sh -o quick_start.sh && bash quick_start.sh`,
  },
  {
    key: "cyberpanel",
    name: "CyberPanel",
    badge: "LiteSpeed · 2k★",
    blurb: "deploy.d.cyberpanel",
    url: "https://cyberpanel.net/",
    command: () => `sh <(curl -s https://cyberpanel.net/install.sh || wget -q -O - https://cyberpanel.net/install.sh)`,
  },
  {
    key: "hestiacp",
    name: "HestiaCP",
    badge: "4.5k★ · OSS",
    blurb: "deploy.d.hestiacp",
    url: "https://hestiacp.com/",
    command: () =>
      `wget https://raw.githubusercontent.com/hestiacp/hestiacp/release/install/hst-install.sh && bash hst-install.sh`,
  },
  {
    key: "cloudpanel",
    name: "CloudPanel",
    badge: "WP-tuned · 1.9k★",
    blurb: "deploy.d.cloudpanel",
    url: "https://www.cloudpanel.io/",
    command: () => `curl -sSL https://installer.cloudpanel.io/ce/v2/install.sh -o install.sh && sudo bash install.sh`,
  },
  {
    key: "fastpanel",
    name: "FastPanel",
    badge: "Free · port 8888",
    blurb: "deploy.d.fastpanel",
    url: "https://fastpanel.direct/",
    command: () => `wget https://repo.fastpanel.direct/install_fastpanel.sh -O - | bash -`,
  },
  {
    key: "lemp",
    name: "LEMP + WordPress",
    badge: "No panel · wp-cli",
    blurb: "deploy.d.lemp",
    url: "https://wordpress.org/",
    needsDomain: true,
    command: ({ domain }) => lempScript(domain),
  },
];

/** A self-contained, non-interactive LEMP + WordPress install, run as one heredoc. */
function lempScript(domain: string): string {
  const d = domain.trim() || "$(hostname -I | awk '{print $1}')";
  return `sudo bash <<'TWLEMP'
set -e
export DEBIAN_FRONTEND=noninteractive
DOMAIN="${d}"
WPDIR="/var/www/wordpress"
DBNAME="wordpress"
DBUSER="wp$(tr -dc 'a-z0-9' </dev/urandom | head -c6)"
DBPASS="$(tr -dc 'A-Za-z0-9' </dev/urandom | head -c20)"
apt-get update -y
apt-get install -y nginx mariadb-server php-fpm php-mysql php-curl php-gd php-xml php-mbstring php-zip php-intl curl unzip
curl -sSLo /usr/local/bin/wp https://raw.githubusercontent.com/wp-cli/builds/gh-pages/phar/wp-cli.phar
chmod +x /usr/local/bin/wp
systemctl enable --now mariadb
mysql -e "CREATE DATABASE IF NOT EXISTS \\\`$DBNAME\\\`; CREATE USER IF NOT EXISTS '$DBUSER'@'localhost' IDENTIFIED BY '$DBPASS'; GRANT ALL ON \\\`$DBNAME\\\`.* TO '$DBUSER'@'localhost'; FLUSH PRIVILEGES;"
mkdir -p "$WPDIR"
wp core download --path="$WPDIR" --allow-root
wp config create --path="$WPDIR" --dbname="$DBNAME" --dbuser="$DBUSER" --dbpass="$DBPASS" --allow-root
chown -R www-data:www-data "$WPDIR"
PHPSOCK=$(ls /run/php/php*-fpm.sock 2>/dev/null | head -n1)
cat >/etc/nginx/sites-available/wordpress <<NGINX
server {
  listen 80;
  server_name $DOMAIN;
  root $WPDIR;
  index index.php index.html;
  location / { try_files \\$uri \\$uri/ /index.php?\\$args; }
  location ~ \\.php\\$ { include snippets/fastcgi-php.conf; fastcgi_pass unix:$PHPSOCK; }
}
NGINX
ln -sf /etc/nginx/sites-available/wordpress /etc/nginx/sites-enabled/wordpress
rm -f /etc/nginx/sites-enabled/default
nginx -t && systemctl reload nginx
echo
echo "=================================================="
echo " WordPress is ready. Finish setup in a browser:"
echo "   http://$DOMAIN/"
echo " Database: $DBNAME   user: $DBUSER   pass: $DBPASS"
echo "=================================================="
TWLEMP`;
}

export function DeployView({ hostId }: { hostId: string }) {
  const t = useT();
  const host = useApp((s) => s.hosts.find((h) => h.id === hostId));
  const [domain, setDomain] = useState("");

  if (!host) {
    return (
      <div className="page">
        <div className="empty">{t("overview.noMatch")}</div>
      </div>
    );
  }

  const run = (tgt: Target) => {
    const command = tgt.command({ domain });
    confirmAction({
      title: t("deploy.confirmTitle", { name: tgt.name, host: host.name }),
      body: t("deploy.confirmBody"),
      confirm: t("deploy.run"),
      run: () => {
        void openTerminal(host.id, { command });
        toast("info", t("deploy.started", { name: tgt.name }));
      },
    });
  };

  return (
    <div className="page">
      <div className="page-head">
        <div style={{ minWidth: 0 }}>
          <div className="row" style={{ gap: 10 }}>
            <h1 className="truncate">{t("deploy.title")}</h1>
            <button
              style={{
                background: "none",
                border: 0,
                padding: 0,
                font: "inherit",
                color: "var(--accent-ink)",
                cursor: "pointer",
              }}
              className="truncate"
              onClick={() => navigate({ name: "host", hostId })}
            >
              {host.name}
            </button>
          </div>
          <p className="truncate muted" style={{ marginTop: 2 }}>
            {t("deploy.subtitle")}
          </p>
        </div>
      </div>

      <div className="callout info" style={{ marginTop: 16 }}>
        <Rocket size={16} />
        <div className="grow">{t("deploy.note")}</div>
      </div>

      <div className="stack" style={{ marginTop: 16, gap: 14 }}>
        {TARGETS.map((tgt, i) => (
          <div className="card card-pad" key={tgt.key}>
            <div className="row" style={{ gap: 10, alignItems: "baseline", flexWrap: "wrap" }}>
              <span className="faint" style={{ fontVariantNumeric: "tabular-nums", minWidth: 20 }}>
                #{i + 1}
              </span>
              <strong style={{ fontSize: 16 }}>{tgt.name}</strong>
              <span className="badge">{tgt.badge}</span>
              <span className="badge ok">{t("deploy.free")}</span>
              <a
                className="row faint"
                href={tgt.url}
                target="_blank"
                rel="noopener noreferrer"
                style={{ gap: 4, fontSize: 12.5, marginLeft: "auto" }}
              >
                {t("deploy.site")} <ExternalLink size={12} />
              </a>
            </div>
            <p className="muted" style={{ margin: "8px 0 10px" }}>
              {t(tgt.blurb)}
            </p>

            {tgt.needsDomain && (
              <div className="field" style={{ marginBottom: 10, maxWidth: 360 }}>
                <label>{t("deploy.domain")}</label>
                <input
                  value={domain}
                  placeholder={t("deploy.domainPlaceholder")}
                  onChange={(e) => setDomain(e.target.value)}
                />
              </div>
            )}

            <pre
              className="mono selectable"
              style={{
                background: "var(--surface-2, rgba(0,0,0,0.04))",
                border: "1px solid var(--border)",
                borderRadius: 8,
                padding: "10px 12px",
                fontSize: 12,
                overflowX: "auto",
                whiteSpace: "pre",
                margin: "0 0 12px",
                maxHeight: tgt.needsDomain ? 220 : 80,
              }}
            >
              {tgt.command({ domain })}
            </pre>

            <div className="row" style={{ gap: 8 }}>
              <button className="btn primary" onClick={() => run(tgt)}>
                <SquareTerminal size={15} />
                {t("deploy.install")}
              </button>
              <button className="btn" onClick={() => void copyText(tgt.command({ domain }), t("common.copied"))}>
                <Copy size={14} />
                {t("deploy.copy")}
              </button>
            </div>
          </div>
        ))}
      </div>
    </div>
  );
}
