package io.github.nguyenquocanhz.termward;

import android.app.Notification;
import android.app.Service;
import android.content.Intent;
import android.content.pm.ServiceInfo;
import android.os.Build;
import android.os.IBinder;
import android.util.Log;

import androidx.core.app.NotificationCompat;

import java.util.Locale;

/**
 * Optional foreground service ("Monitor in the background" in Settings). It only
 * keeps the process alive; the Go core does the checking and alerting.
 */
public class MonitorService extends Service {
    private static final int ID = 1;

    @Override
    public int onStartCommand(Intent intent, int flags, int startId) {
        Notifications.createChannels(this);
        Notification n = new NotificationCompat.Builder(this, Notifications.MONITOR)
                .setSmallIcon(R.drawable.ic_stat_termward)
                .setColor(Notifications.COLOR)
                .setContentTitle(getString(R.string.monitor_title))
                .setContentText(getString(R.string.monitor_text))
                .setOngoing(true)
                .setPriority(NotificationCompat.PRIORITY_MIN)
                .setContentIntent(Notifications.openApp(this, null))
                .build();
        if (Build.VERSION.SDK_INT >= 34) {
            startForeground(ID, n, ServiceInfo.FOREGROUND_SERVICE_TYPE_SPECIAL_USE);
        } else {
            startForeground(ID, n);
        }
        try {
            TermwardCore.ensureStarted(this, Locale.getDefault().getLanguage());
        } catch (Exception e) {
            Log.e("MonitorService", "core failed to start", e);
            stopSelf();
        }
        return START_STICKY;
    }

    @Override
    public IBinder onBind(Intent intent) {
        return null;
    }
}
