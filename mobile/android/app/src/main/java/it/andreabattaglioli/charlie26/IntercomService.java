package it.andreabattaglioli.charlie26;

import android.app.Notification;
import android.app.NotificationChannel;
import android.app.NotificationManager;
import android.app.PendingIntent;
import android.app.Service;
import android.content.Context;
import android.content.Intent;
import android.content.pm.ServiceInfo;
import android.os.Build;
import android.os.IBinder;

import androidx.core.app.NotificationCompat;
import androidx.core.app.ServiceCompat;

/**
 * Servizio in primo piano: finché si è nell'intercom Android non spegne il
 * microfono né l'audio quando si passa a un'altra app. La notifica mostra lo
 * stato e ha il tasto Parla / Muto.
 */
public class IntercomService extends Service {
    static final String CHANNEL = "intercom";
    static final int ID = 26;
    static final String EXTRA_TITLE = "titolo";
    static final String EXTRA_TEXT = "testo";
    static final String EXTRA_MIC = "mic";

    static void update(Context ctx, String title, String text, boolean micOn) {
        Intent i = new Intent(ctx, IntercomService.class);
        i.putExtra(EXTRA_TITLE, title);
        i.putExtra(EXTRA_TEXT, text);
        i.putExtra(EXTRA_MIC, micOn);
        try {
            androidx.core.content.ContextCompat.startForegroundService(ctx, i);
        } catch (Exception ignored) {
            // es. avvio non permesso dallo sfondo: riproverà al prossimo aggiornamento
        }
    }

    static void stop(Context ctx) {
        ctx.stopService(new Intent(ctx, IntercomService.class));
    }

    @Override
    public int onStartCommand(Intent intent, int flags, int startId) {
        String title = intent != null ? intent.getStringExtra(EXTRA_TITLE) : null;
        String text = intent != null ? intent.getStringExtra(EXTRA_TEXT) : null;
        boolean mic = intent != null && intent.getBooleanExtra(EXTRA_MIC, false);
        Notification n = build(title != null ? title : "CHARLIE26 attivo", text != null ? text : "", mic);
        int type = 0;
        if (Build.VERSION.SDK_INT >= 30) {
            type = ServiceInfo.FOREGROUND_SERVICE_TYPE_MICROPHONE | ServiceInfo.FOREGROUND_SERVICE_TYPE_MEDIA_PLAYBACK;
        }
        try {
            ServiceCompat.startForeground(this, ID, n, type);
        } catch (Exception e) {
            // Senza il permesso del microfono (o avvio dallo sfondo) il servizio non può partire.
            stopSelf();
        }
        return START_NOT_STICKY;
    }

    private Notification build(String title, String text, boolean micOn) {
        NotificationManager nm = (NotificationManager) getSystemService(NOTIFICATION_SERVICE);
        if (Build.VERSION.SDK_INT >= 26 && nm.getNotificationChannel(CHANNEL) == null) {
            NotificationChannel ch = new NotificationChannel(CHANNEL, "Intercom attivo", NotificationManager.IMPORTANCE_LOW);
            ch.setDescription("Resta visibile mentre sei collegato all'intercom");
            ch.setShowBadge(false);
            nm.createNotificationChannel(ch);
        }
        int pf = PendingIntent.FLAG_UPDATE_CURRENT | PendingIntent.FLAG_IMMUTABLE;
        Intent open = new Intent(this, MainActivity.class).addFlags(Intent.FLAG_ACTIVITY_SINGLE_TOP);
        Intent toggle = new Intent(MainActivity.ACTION_TOGGLE_MIC).setPackage(getPackageName());
        return new NotificationCompat.Builder(this, CHANNEL)
                .setSmallIcon(R.drawable.ic_stat_intercom)
                .setContentTitle(title)
                .setContentText(text)
                .setOngoing(true)
                .setOnlyAlertOnce(true)
                .setSilent(true)
                .setCategory(NotificationCompat.CATEGORY_CALL)
                .setColor(micOn ? 0xFFF26E1A : 0xFF2B8F9B)
                .setContentIntent(PendingIntent.getActivity(this, 0, open, pf))
                .addAction(0, micOn ? "Muto" : "Parla", PendingIntent.getBroadcast(this, 1, toggle, pf))
                .build();
    }

    @Override
    public IBinder onBind(Intent intent) {
        return null;
    }
}
