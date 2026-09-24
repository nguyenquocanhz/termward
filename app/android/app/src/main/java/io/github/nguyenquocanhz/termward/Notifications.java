package io.github.nguyenquocanhz.termward;

import android.Manifest;
import android.app.Notification;
import android.app.NotificationChannel;
import android.app.NotificationManager;
import android.app.PendingIntent;
import android.content.Context;
import android.content.Intent;
import android.content.pm.PackageManager;
import android.os.Build;

import androidx.core.app.NotificationCompat;
import androidx.core.app.NotificationManagerCompat;
import androidx.core.content.ContextCompat;

/** Health alerts posted straight from the Go core, even while the UI is paused. */
final class Notifications {
    static final String ALERTS = "alerts";
    static final String MONITOR = "monitor";
    static final int COLOR = 0xFFC96442;

    private Notifications() {}

    static void createChannels(Context c) {
        if (Build.VERSION.SDK_INT < 26) return;
        NotificationManager nm = c.getSystemService(NotificationManager.class);
        NotificationChannel alerts = new NotificationChannel(ALERTS,
                c.getString(R.string.channel_alerts), NotificationManager.IMPORTANCE_HIGH);
        alerts.setDescription(c.getString(R.string.channel_alerts_desc));
        nm.createNotificationChannel(alerts);
        NotificationChannel monitor = new NotificationChannel(MONITOR,
                c.getString(R.string.channel_monitor), NotificationManager.IMPORTANCE_MIN);
        monitor.setShowBadge(false);
        nm.createNotificationChannel(monitor);
    }

    static PendingIntent openApp(Context c, String hostId) {
        Intent i = new Intent(c, MainActivity.class)
                .setFlags(Intent.FLAG_ACTIVITY_SINGLE_TOP | Intent.FLAG_ACTIVITY_CLEAR_TOP);
        if (hostId != null) i.putExtra("hostId", hostId);
        return PendingIntent.getActivity(c, hostId == null ? 0 : hostId.hashCode(), i,
                PendingIntent.FLAG_UPDATE_CURRENT | PendingIntent.FLAG_IMMUTABLE);
    }

    static void alert(Context c, String title, String body, String hostId) {
        if (Build.VERSION.SDK_INT >= 33 && ContextCompat.checkSelfPermission(c,
                Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED) {
            return;
        }
        Notification n = new NotificationCompat.Builder(c, ALERTS)
                .setSmallIcon(R.drawable.ic_stat_termward)
                .setColor(COLOR)
                .setContentTitle(title)
                .setContentText(body)
                .setStyle(new NotificationCompat.BigTextStyle().bigText(body))
                .setPriority(NotificationCompat.PRIORITY_HIGH)
                .setCategory(NotificationCompat.CATEGORY_STATUS)
                .setAutoCancel(true)
                .setContentIntent(openApp(c, hostId))
                .build();
        // One notification per server: a newer state replaces the older one.
        NotificationManagerCompat.from(c).notify(hostId.hashCode(), n);
    }
}
