package io.github.nguyenquocanhz.termward;

import android.annotation.SuppressLint;
import android.content.Context;
import android.os.Build;
import android.provider.Settings;
import android.util.Log;

import java.io.File;
import java.util.Locale;

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
        // Termward Pro binds this install to the device: ANDROID_ID is stable per
        // app signing key, user and device (Android 8+), and the core only ever
        // sends a hash of it.
        Mobile.setMachineID(androidId(app));
        Mobile.setDeviceName(deviceName());
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

    @SuppressLint("HardwareIds")
    private static String androidId(Context app) {
        try {
            String id = Settings.Secure.getString(app.getContentResolver(), Settings.Secure.ANDROID_ID);
            return id != null ? id : "";
        } catch (Exception e) {
            Log.w(TAG, "ANDROID_ID unavailable", e);
            return ""; // the core falls back to a random id kept in its vault
        }
    }

    private static String deviceName() {
        String maker = Build.MANUFACTURER == null ? "" : Build.MANUFACTURER;
        String model = Build.MODEL == null ? "" : Build.MODEL;
        if (!maker.isEmpty() && model.toLowerCase(Locale.ROOT).startsWith(maker.toLowerCase(Locale.ROOT))) return model;
        return (maker.isEmpty() ? "" : capitalize(maker) + " ") + model;
    }

    private static String capitalize(String s) {
        return s.isEmpty() ? s : s.substring(0, 1).toUpperCase(Locale.ROOT) + s.substring(1);
    }

    public static synchronized long port() {
        return port;
    }

    public static synchronized String token() {
        return token;
    }
}
