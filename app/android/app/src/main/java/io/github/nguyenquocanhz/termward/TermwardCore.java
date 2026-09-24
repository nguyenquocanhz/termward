package io.github.nguyenquocanhz.termward;

import android.content.Context;
import android.util.Log;

import java.io.File;

import io.github.nguyenquocanhz.termward.mobile.Mobile;

/**
 * Starts the Go core (built with gomobile from core/mobile) once per process.
 * The WebView UI and the background MonitorService share the same instance.
 */
public final class TermwardCore {
    private static final String TAG = "TermwardCore";
    private static long port = 0;
    private static String token = "";

    private TermwardCore() {}

    public static synchronized void ensureStarted(Context ctx, String language) throws Exception {
        if (port != 0) return;
        Context app = ctx.getApplicationContext();
        Notifications.createChannels(app);
        File dir = new File(app.getFilesDir(), "core");
        //noinspection ResultOfMethodCallIgnored
        dir.mkdirs();
        String vaultKey = VaultKey.get(app);
        try {
            port = Mobile.start(dir.getAbsolutePath(), vaultKey, language, (title, body, hostId) ->
                    Notifications.alert(app, title, body, hostId));
        } catch (Exception e) {
            // The keystore key can be lost (e.g. restored backup): drop the
            // encrypted secrets so the app still starts; users re-enter passwords.
            if (e.getMessage() != null && e.getMessage().contains("decrypted")) {
                Log.w(TAG, "vault unreadable, resetting remembered secrets");
                //noinspection ResultOfMethodCallIgnored
                new File(dir, "secrets.bin").delete();
                port = Mobile.start(dir.getAbsolutePath(), vaultKey, language, (title, body, hostId) ->
                        Notifications.alert(app, title, body, hostId));
            } else {
                throw e;
            }
        }
        token = Mobile.token();
        Log.i(TAG, "core " + Mobile.version() + " listening on 127.0.0.1:" + port);
    }

    public static synchronized long port() {
        return port;
    }

    public static synchronized String token() {
        return token;
    }
}
