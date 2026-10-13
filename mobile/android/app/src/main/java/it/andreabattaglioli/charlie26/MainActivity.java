package it.andreabattaglioli.charlie26;

import android.Manifest;
import android.app.PendingIntent;
import android.app.PictureInPictureParams;
import android.app.RemoteAction;
import android.content.BroadcastReceiver;
import android.content.ContentValues;
import android.content.Context;
import android.content.Intent;
import android.content.IntentFilter;
import android.content.SharedPreferences;
import android.content.pm.PackageManager;
import android.content.res.Configuration;
import android.graphics.drawable.Icon;
import android.net.Uri;
import android.os.Build;
import android.os.Bundle;
import android.provider.MediaStore;
import android.util.Rational;
import android.webkit.CookieManager;
import android.webkit.JavascriptInterface;
import android.webkit.WebView;
import android.widget.Toast;

import androidx.core.content.ContextCompat;

import com.getcapacitor.BridgeActivity;

import org.json.JSONArray;
import org.json.JSONObject;

import java.io.OutputStream;
import java.nio.charset.StandardCharsets;
import java.util.HashSet;
import java.util.Set;

/**
 * App CHARLIE26: all'avvio la pagina interna legge il QR; poi il WebView apre
 * l'intercom del PC di regia. Qui la parte nativa: certificato dell'evento,
 * servizio in secondo piano, pop-up (Picture-in-Picture) con il tasto parla.
 */
public class MainActivity extends BridgeActivity {
    static final String ACTION_TOGGLE_MIC = "it.andreabattaglioli.charlie26.TOGGLE_MIC";
    private static final String START_URL = "https://localhost/";

    EventTrust trust;
    private SharedPreferences prefs;
    private volatile boolean joined = false;
    private volatile boolean micOn = false;
    private volatile boolean hasVideo = false;
    private boolean inPip = false;

    private final BroadcastReceiver toggleReceiver = new BroadcastReceiver() {
        @Override
        public void onReceive(Context context, Intent intent) {
            js("window.intercomPip && window.intercomPip.toggleMic()");
        }
    };

    @Override
    public void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);
        trust = new EventTrust(this);
        prefs = getSharedPreferences("charlie26.eventi", MODE_PRIVATE);
        WebView wv = bridge.getWebView();
        bridge.setWebViewClient(new PinnedWebViewClient(bridge, this));
        wv.addJavascriptInterface(new NativeApi(), "charlieApp");
        wv.getSettings().setMediaPlaybackRequiresUserGesture(false);
        CookieManager.getInstance().setAcceptCookie(true);
        ContextCompat.registerReceiver(this, toggleReceiver, new IntentFilter(ACTION_TOGGLE_MIC), ContextCompat.RECEIVER_NOT_EXPORTED);
        if (Build.VERSION.SDK_INT >= 33 && ContextCompat.checkSelfPermission(this, Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED) {
            requestPermissions(new String[]{Manifest.permission.POST_NOTIFICATIONS}, 1);
        }
    }

    @Override
    public void onDestroy() {
        try {
            unregisterReceiver(toggleReceiver);
        } catch (Exception ignored) {
        }
        IntercomService.stop(this);
        super.onDestroy();
    }

    @Override
    public void onPause() {
        super.onPause();
        CookieManager.getInstance().flush();
        // L'intercom continua: il WebView non va messo in pausa.
        bridge.getWebView().onResume();
        bridge.getWebView().resumeTimers();
    }

    @Override
    public void onStop() {
        super.onStop();
        if (joined) {
            bridge.getWebView().onResume();
            bridge.getWebView().resumeTimers();
        }
    }

    // ---------- Pop-up (Picture-in-Picture) ----------

    @Override
    protected void onUserLeaveHint() {
        super.onUserLeaveHint();
        // Prima di Android 12 il pop-up si apre qui; dopo, in automatico (setAutoEnterEnabled).
        if (joined && Build.VERSION.SDK_INT >= 26 && Build.VERSION.SDK_INT < 31) enterPip();
    }

    void enterPip() {
        if (Build.VERSION.SDK_INT < 26 || !getPackageManager().hasSystemFeature(PackageManager.FEATURE_PICTURE_IN_PICTURE)) return;
        try {
            enterPictureInPictureMode(pipParams());
        } catch (Exception ignored) {
        }
    }

    private PictureInPictureParams pipParams() {
        PictureInPictureParams.Builder b = new PictureInPictureParams.Builder()
                .setAspectRatio(new Rational(16, 9));
        Intent toggle = new Intent(ACTION_TOGGLE_MIC).setPackage(getPackageName());
        PendingIntent pi = PendingIntent.getBroadcast(this, 2, toggle, PendingIntent.FLAG_UPDATE_CURRENT | PendingIntent.FLAG_IMMUTABLE);
        Icon icon = Icon.createWithResource(this, micOn ? R.drawable.ic_pip_mic_on : R.drawable.ic_pip_mic_off);
        String label = micOn ? "Muto" : "Parla";
        java.util.ArrayList<RemoteAction> actions = new java.util.ArrayList<>();
        actions.add(new RemoteAction(icon, label, label, pi));
        b.setActions(actions);
        if (Build.VERSION.SDK_INT >= 31) {
            b.setAutoEnterEnabled(joined);
            b.setSeamlessResizeEnabled(false);
        }
        return b.build();
    }

    private void refreshPip() {
        if (Build.VERSION.SDK_INT < 26) return;
        try {
            setPictureInPictureParams(pipParams());
        } catch (Exception ignored) {
        }
    }

    @Override
    public void onPictureInPictureModeChanged(boolean isInPip, Configuration newConfig) {
        super.onPictureInPictureModeChanged(isInPip, newConfig);
        inPip = isInPip;
        js("window.charliePipMode && window.charliePipMode(" + isInPip + ")");
    }

    // ---------- Utilità ----------

    void js(String code) {
        runOnUiThread(() -> bridge.getWebView().evaluateJavascript(code, null));
    }

    void showStartError(String msg) {
        runOnUiThread(() -> {
            WebView wv = bridge.getWebView();
            String url = wv.getUrl();
            if (url == null || !url.startsWith(START_URL)) {
                wv.loadUrl(START_URL);
                // la pagina iniziale deve essere pronta prima di mostrare l'errore
                wv.postDelayed(() -> js("window.startError && window.startError(" + JSONObject.quote(msg) + ")"), 900);
            } else {
                js("window.startError && window.startError(" + JSONObject.quote(msg) + ")");
            }
        });
    }

    boolean isEventUrl(String url) {
        String k = EventTrust.key(url);
        return !k.isEmpty() && prefs.getStringSet("aperti", new HashSet<>()).contains(k);
    }

    private void rememberEvent(String url) {
        Set<String> s = new HashSet<>(prefs.getStringSet("aperti", new HashSet<>()));
        s.add(EventTrust.key(url));
        prefs.edit().putStringSet("aperti", s).apply();
    }

    /** Metodi chiamabili dalle pagine (window.charlieApp). */
    class NativeApi {
        @JavascriptInterface
        public boolean isApp() {
            return true;
        }

        /** Apre l'intercom: prima verifica il certificato con l'impronta del QR. */
        @JavascriptInterface
        public void open(String url, String pin) {
            if (url == null || !url.startsWith("https://")) {
                showStartError("Indirizzo non valido.");
                return;
            }
            new Thread(() -> {
                if (pin != null && !pin.isEmpty()) {
                    String err = trust.learn(url, pin);
                    if (err != null) {
                        showStartError(err);
                        return;
                    }
                }
                rememberEvent(url);
                runOnUiThread(() -> bridge.getWebView().loadUrl(url));
            }).start();
        }

        /** Torna alla pagina iniziale (lettura QR). */
        @JavascriptInterface
        public void home() {
            joined = false;
            IntercomService.stop(MainActivity.this);
            runOnUiThread(() -> bridge.getWebView().loadUrl(START_URL));
        }

        /** Stato dell'intercom: notifica e pop-up. */
        @JavascriptInterface
        public void update(String json) {
            try {
                JSONObject o = new JSONObject(json);
                joined = o.optBoolean("joined");
                micOn = o.optBoolean("micOn");
                hasVideo = o.optBoolean("video");
                if (!joined) {
                    IntercomService.stop(MainActivity.this);
                } else if (ContextCompat.checkSelfPermission(MainActivity.this, Manifest.permission.RECORD_AUDIO) == PackageManager.PERMISSION_GRANTED) {
                    String title = micOn ? "● IN ONDA · " + join(o.optJSONArray("talk")) : "CHARLIE26 · MUTO";
                    String who = join(o.optJSONArray("speaking"));
                    String text = who.isEmpty() ? join(o.optJSONArray("channels")) : "Parla: " + who;
                    IntercomService.update(MainActivity.this, title, text, micOn);
                }
                runOnUiThread(MainActivity.this::refreshPip);
            } catch (Exception ignored) {
            }
        }

        @JavascriptInterface
        public void enterPip() {
            runOnUiThread(MainActivity.this::enterPip);
        }

        @JavascriptInterface
        public boolean inPip() {
            return inPip;
        }

        /** Salva un file di testo (es. esporta configurazione) nella cartella Download. */
        @JavascriptInterface
        public void saveFile(String name, String content) {
            runOnUiThread(() -> {
                try {
                    if (Build.VERSION.SDK_INT >= 29) {
                        ContentValues v = new ContentValues();
                        v.put(MediaStore.Downloads.DISPLAY_NAME, name);
                        v.put(MediaStore.Downloads.MIME_TYPE, "application/json");
                        Uri uri = getContentResolver().insert(MediaStore.Downloads.EXTERNAL_CONTENT_URI, v);
                        try (OutputStream out = getContentResolver().openOutputStream(uri)) {
                            out.write(content.getBytes(StandardCharsets.UTF_8));
                        }
                        Toast.makeText(MainActivity.this, "Salvato in Download: " + name, Toast.LENGTH_LONG).show();
                    } else {
                        Intent send = new Intent(Intent.ACTION_SEND).setType("application/json").putExtra(Intent.EXTRA_TEXT, content).putExtra(Intent.EXTRA_TITLE, name);
                        startActivity(Intent.createChooser(send, name));
                    }
                } catch (Exception e) {
                    Toast.makeText(MainActivity.this, "Salvataggio non riuscito: " + e.getMessage(), Toast.LENGTH_LONG).show();
                }
            });
        }

        @JavascriptInterface
        public void openExternal(String url) {
            if (url == null || !url.startsWith("https://")) return;
            runOnUiThread(() -> {
                try {
                    startActivity(new Intent(Intent.ACTION_VIEW, Uri.parse(url)));
                } catch (Exception ignored) {
                }
            });
        }

        /** Chiude l'app (l'intercom resta acceso per gli altri). */
        @JavascriptInterface
        public void exitApp() {
            joined = false;
            IntercomService.stop(MainActivity.this);
            runOnUiThread(MainActivity.this::finishAndRemoveTask);
        }
    }

    private static String join(JSONArray a) {
        if (a == null) return "";
        StringBuilder sb = new StringBuilder();
        for (int i = 0; i < a.length(); i++) {
            if (i > 0) sb.append(", ");
            sb.append(a.optString(i));
        }
        return sb.toString();
    }
}
