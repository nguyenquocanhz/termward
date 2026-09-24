package io.github.nguyenquocanhz.termward;

import android.Manifest;
import android.content.Context;
import android.content.Intent;
import android.content.SharedPreferences;

import androidx.core.content.ContextCompat;

import com.getcapacitor.JSObject;
import com.getcapacitor.Plugin;
import com.getcapacitor.PluginCall;
import com.getcapacitor.PluginMethod;
import com.getcapacitor.annotation.CapacitorPlugin;
import com.getcapacitor.annotation.Permission;

import io.github.nguyenquocanhz.termward.mobile.Mobile;

/** Bridge used by app/src/lib/mobile.ts. */
@CapacitorPlugin(
        name = "TermwardCore",
        permissions = {@Permission(strings = {Manifest.permission.POST_NOTIFICATIONS}, alias = "notifications")})
public class TermwardCorePlugin extends Plugin {
    static TermwardCorePlugin instance;
    private static final String BG = "background";

    @Override
    public void load() {
        instance = this;
        if (prefs().getBoolean(BG, false)) startMonitor();
        handleIntent(getActivity().getIntent());
    }

    @PluginMethod
    public void start(PluginCall call) {
        String language = call.getString("language", "en");
        new Thread(() -> {
            try {
                TermwardCore.ensureStarted(getContext(), language);
                JSObject r = new JSObject();
                r.put("port", TermwardCore.port());
                r.put("token", TermwardCore.token());
                call.resolve(r);
            } catch (Exception e) {
                call.reject("Termward core failed to start: " + e.getMessage(), e);
            }
        }).start();
    }

    @PluginMethod
    public void setLanguage(PluginCall call) {
        Mobile.setLanguage(call.getString("language", "en"));
        call.resolve();
    }

    @PluginMethod
    public void getBackground(PluginCall call) {
        JSObject r = new JSObject();
        r.put("enabled", prefs().getBoolean(BG, false));
        call.resolve(r);
    }

    @PluginMethod
    public void setBackground(PluginCall call) {
        boolean on = Boolean.TRUE.equals(call.getBoolean("enabled", false));
        prefs().edit().putBoolean(BG, on).apply();
        if (on) startMonitor();
        else getContext().stopService(new Intent(getContext(), MonitorService.class));
        call.resolve();
    }

    /** Called for launches and re-launches from an alert notification. */
    void handleIntent(Intent intent) {
        if (intent == null) return;
        String hostId = intent.getStringExtra("hostId");
        if (hostId == null) return;
        intent.removeExtra("hostId");
        JSObject d = new JSObject();
        d.put("hostId", hostId);
        notifyListeners("notificationTap", d, true);
    }

    private void startMonitor() {
        ContextCompat.startForegroundService(getContext(), new Intent(getContext(), MonitorService.class));
    }

    private SharedPreferences prefs() {
        return getContext().getSharedPreferences("termward", Context.MODE_PRIVATE);
    }
}
