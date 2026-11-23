package com.morelink.app;

import android.os.Bundle;
import android.service.notification.NotificationListenerService;
import android.service.notification.StatusBarNotification;
import android.util.Log;

/** Reads incoming notifications and forwards them to the paired PC. */
public class NotificationListener extends NotificationListenerService {
    private static final String TAG = "MoreLinkNL";
    private long lastForward = 0;

    @Override
    public void onListenerConnected() {
        super.onListenerConnected();
        Log.i(TAG, "notification listener connected");
    }

    @Override
    public void onNotificationPosted(StatusBarNotification sbn) {
        try {
            // Ignore our own and system notifications, throttle to avoid spam.
            String pkg = sbn.getPackageName();
            if (pkg == null || pkg.equals("com.morelink.app") || pkg.equals("android")) return;
            long now = System.currentTimeMillis();
            if (now - lastForward < 800) return;
            lastForward = now;

            Bundle extras = sbn.getNotification().extras;
            if (extras == null) return;
            CharSequence title = extras.getCharSequence(android.app.Notification.EXTRA_TITLE);
            CharSequence text = extras.getCharSequence(android.app.Notification.EXTRA_TEXT);
            if (title == null && text == null) return;
            String t = title == null ? "" : title.toString();
            String b = text == null ? "" : text.toString();
            if (t.isEmpty() && b.isEmpty()) return;

            // Resolve app label.
            String app = pkg;
            try {
                android.content.pm.PackageManager pm = getPackageManager();
                android.content.pm.ApplicationInfo ai = pm.getApplicationInfo(pkg, 0);
                CharSequence label = pm.getApplicationLabel(ai);
                if (label != null) app = label.toString();
            } catch (Exception ignored) {}

            LinkService.forwardNotification(this, app, t, b);
        } catch (Exception e) {
            Log.d(TAG, "forward error: " + e.getMessage());
        }
    }
}
