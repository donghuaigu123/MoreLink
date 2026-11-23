package com.morelink.app;

import android.app.Notification;
import android.app.NotificationChannel;
import android.app.NotificationManager;
import android.app.Service;
import android.content.ClipData;
import android.content.ClipboardManager;
import android.content.Context;
import android.content.Intent;
import android.content.SharedPreferences;
import android.os.Build;
import android.os.IBinder;
import android.util.Log;

import java.io.BufferedInputStream;
import java.io.DataOutputStream;
import java.io.File;
import java.io.FileOutputStream;
import java.io.IOException;
import java.io.InputStream;
import java.io.OutputStream;
import java.net.DatagramPacket;
import java.net.DatagramSocket;
import java.net.InetAddress;
import java.net.ServerSocket;
import java.net.Socket;
import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;
import java.util.Arrays;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.atomic.AtomicBoolean;

/** Foreground service: LAN discovery, MLP TCP server, pairing and sync. */
public class LinkService extends Service {
    private static final String TAG = "MoreLink";
    public static final String PREFS = "morelink";
    public static final String ACTION_STATE = "com.morelink.STATE";
    public static final String EXTRA_STATE = "state";

    private final ExecutorService pool = Executors.newCachedThreadPool();
    private final AtomicBoolean running = new AtomicBoolean(false);
    private ServerSocket server;
    private DatagramSocket discovery;
    private final AtomicBoolean stopBeacon = new AtomicBoolean(false);

    private String myId;
    private String myName;
    private byte[] key;
    private boolean paired;
    private String status = "就绪";
    private int notificationCount = 0;
    private int fileCount = 0;

    public static String stateJson(Context c) {
        SharedPreferences p = c.getSharedPreferences(PREFS, Context.MODE_PRIVATE);
        String id = p.getString("id", "");
        boolean paired = p.getBoolean("paired", false);
        String name = p.getString("name", "Android");
        return "{\"id\":\"" + Mlp.jsonEscape(id) + "\",\"name\":\"" + Mlp.jsonEscape(name) +
                "\",\"paired\":" + paired + ",\"pin\":\"" + Mlp.jsonEscape(p.getString("pin", "")) +
                "\",\"status\":\"" + Mlp.jsonEscape(statusStatic) + "\",\"notifications\":" + notificationCountStatic +
                ",\"files\":" + fileCountStatic + "}";
    }

    private static volatile String statusStatic = "就绪";
    private static volatile int notificationCountStatic = 0;
    private static volatile int fileCountStatic = 0;

    @Override
    public void onCreate() {
        super.onCreate();
        SharedPreferences p = getSharedPreferences(PREFS, MODE_PRIVATE);
        myId = p.getString("id", "");
        if (myId.isEmpty()) {
            myId = Mlp.makeId();
            p.edit().putString("id", myId).apply();
        }
        myName = p.getString("name", "Android");
        paired = p.getBoolean("paired", false);
        if (paired) {
            String pin = p.getString("pin", "");
            String peer = p.getString("peer", "");
            key = Mlp.deriveKey(pin, myId, peer);
        }
    }

    @Override
    public int onStartCommand(Intent intent, int flags, int startId) {
        startForeground(1, buildNotification("MoreLink 正在运行"));
        if (running.compareAndSet(false, true)) {
            pool.execute(this::discoveryLoop);
            pool.execute(this::serverLoop);
        }
        return START_STICKY;
    }

    private Notification buildNotification(String text) {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            NotificationChannel ch = new NotificationChannel("morelink", "MoreLink",
                    NotificationManager.IMPORTANCE_LOW);
            NotificationManager nm = getSystemService(NotificationManager.class);
            if (nm != null) nm.createNotificationChannel(ch);
            return new Notification.Builder(this, "morelink")
                    .setContentTitle("MoreLink")
                    .setContentText(text)
                    .setSmallIcon(android.R.drawable.stat_sys_data_bluetooth)
                    .build();
        }
        return new Notification.Builder(this)
                .setContentTitle("MoreLink")
                .setContentText(text)
                .setSmallIcon(android.R.drawable.stat_sys_data_bluetooth)
                .build();
    }

    private void setStatus(String s) {
        status = s;
        statusStatic = s;
        sendBroadcast(new Intent(ACTION_STATE));
    }

    /** LAN discovery: broadcast hello and listen for peers. */
    private void discoveryLoop() {
        byte[] hello = Mlp.helloJson(myId, myName, "android", Mlp.DATA_PORT).getBytes(StandardCharsets.UTF_8);
        try {
            discovery = new DatagramSocket(null);
            discovery.setReuseAddress(true);
            discovery.bind(new java.net.InetSocketAddress(Mlp.DISCOVERY_PORT));
        } catch (Exception e) {
            Log.e(TAG, "discovery bind", e);
            return;
        }
        InetAddress broadcast;
        try {
            broadcast = InetAddress.getByName("255.255.255.255");
        } catch (Exception e) {
            return;
        }
        // Beacon thread
        pool.execute(() -> {
            while (!stopBeacon.get()) {
                try {
                    DatagramPacket pkt = new DatagramPacket(hello, hello.length, broadcast, Mlp.DISCOVERY_PORT);
                    discovery.send(pkt);
                } catch (Exception ignored) {}
                try { Thread.sleep(3000); } catch (InterruptedException e) { break; }
            }
        });
        // Listen for peer announcements (used to populate UI via preferences cache)
        byte[] buf = new byte[2048];
        while (running.get()) {
            try {
                DatagramPacket pkt = new DatagramPacket(buf, buf.length);
                discovery.receive(pkt);
                String json = new String(pkt.getData(), pkt.getOffset(), pkt.getLength(), StandardCharsets.UTF_8);
                String id = Mlp.jsonString(json, "id");
                String name = Mlp.jsonString(json, "name");
                String platform = Mlp.jsonString(json, "platform");
                if (id == null || id.equals(myId)) continue;
                PeerStore.update(this, id, name, platform, pkt.getAddress().getHostAddress());
                sendBroadcast(new Intent(ACTION_STATE));
            } catch (Exception e) {
                if (!running.get()) break;
            }
        }
    }

    /** TCP server: accept peer connections and process frames. */
    private void serverLoop() {
        try {
            server = new ServerSocket(Mlp.DATA_PORT);
            server.setReuseAddress(true);
        } catch (Exception e) {
            Log.e(TAG, "server bind", e);
            setStatus("端口占用: " + e.getMessage());
            return;
        }
        setStatus("运行中");
        while (running.get()) {
            try {
                Socket c = server.accept();
                pool.execute(() -> handleConnection(c));
            } catch (Exception e) {
                if (!running.get()) break;
            }
        }
    }

    private void handleConnection(Socket socket) {
        try {
            socket.setSoTimeout(30000);
            OutputStream os = socket.getOutputStream();
            InputStream is = new BufferedInputStream(socket.getInputStream());

            // Plaintext HELLO handshake.
            byte[] hello = Mlp.helloJson(myId, myName, "android", Mlp.DATA_PORT).getBytes(StandardCharsets.UTF_8);
            Mlp.writeFrame(os, Mlp.TYPE_HELLO, hello, null);
            byte[] frame = Mlp.readFrame(is, null);
            if (frame == null || Mlp.frameType(frame) != Mlp.TYPE_HELLO) return;
            String peerJson = Mlp.str(Mlp.framePayload(frame));
            String peerId = Mlp.jsonString(peerJson, "id");
            String peerName = Mlp.jsonString(peerJson, "name");
            if (peerId == null) return;
            PeerStore.update(this, peerId, peerName, "windows", socket.getInetAddress().getHostAddress());

            final byte[] sessionKey;
            if (paired && key != null) {
                sessionKey = key;
            } else {
                // Wait for plaintext pairing request.
                frame = Mlp.readFrame(is, null);
                if (frame == null || Mlp.frameType(frame) != Mlp.TYPE_PAIR_REQ) return;
                String req = Mlp.str(Mlp.framePayload(frame));
                String pin = Mlp.jsonString(req, "pin");
                String reqId = Mlp.jsonString(req, "id");
                if (pin == null || pin.length() != 6) return;
                SharedPreferences p = getSharedPreferences(PREFS, MODE_PRIVATE);
                p.edit().putBoolean("paired", true).putString("pin", pin)
                        .putString("peer", reqId == null ? peerId : reqId).apply();
                paired = true;
                key = Mlp.deriveKey(pin, myId, reqId == null ? peerId : reqId);
                sessionKey = key;
                String result = "{\"ok\":true,\"token\":\"ok\"}";
                Mlp.writeFrame(os, Mlp.TYPE_PAIR_RESULT, Mlp.utf8(result), null);
                setStatus("已配对");
            }

            socket.setSoTimeout(0);
            while (running.get()) {
                frame = Mlp.readFrame(is, sessionKey);
                if (frame == null) break;
                byte type = Mlp.frameType(frame);
                byte[] payload = Mlp.framePayload(frame);
                switch (type) {
                    case Mlp.TYPE_PING:
                        Mlp.writeFrame(os, Mlp.TYPE_PONG, new byte[0], sessionKey);
                        break;
                    case Mlp.TYPE_CLIP_TEXT:
                        setClipboard(Mlp.str(payload));
                        setStatus("收到剪贴板文本");
                        break;
                    case Mlp.TYPE_FILE_OFFER:
                        String offer = Mlp.str(payload);
                        String xferId = Mlp.jsonString(offer, "id");
                        String fileName = Mlp.jsonString(offer, "name");
                        long size = parseLong(Mlp.jsonString(offer, "size"));
                        receiveFile(os, is, sessionKey, xferId, fileName, size);
                        break;
                }
            }
        } catch (Exception e) {
            Log.d(TAG, "connection ended: " + e.getMessage());
        } finally {
            try { socket.close(); } catch (Exception ignored) {}
        }
    }

    private long parseLong(String s) {
        try { return s == null ? 0 : Long.parseLong(s); } catch (Exception e) { return 0; }
    }

    private void receiveFile(OutputStream os, InputStream is, byte[] sessionKey,
                             String xferId, String fileName, long size) {
        if (xferId == null || fileName == null) return;
        String safe = new File(fileName).getName();
        File dir = new File(getExternalFilesDir(null), "Received");
        dir.mkdirs();
        File dst = new File(dir, safe);
        long got = 0, lastAck = 0;
        try (FileOutputStream fos = new FileOutputStream(dst)) {
            String ack = "{\"id\":\"" + Mlp.jsonEscape(xferId) + "\",\"offset\":0,\"ok\":true}";
            Mlp.writeFrame(os, Mlp.TYPE_FILE_ACK, Mlp.utf8(ack), sessionKey);
            while (got < size) {
                byte[] frame = Mlp.readFrame(is, sessionKey);
                if (frame == null) break;
                if (Mlp.frameType(frame) != Mlp.TYPE_FILE_CHUNK) continue;
                byte[] p = Mlp.framePayload(frame);
                if (p.length < 20) continue;
                long offset = ((long)(p[16] & 0xFF) << 24) | ((long)(p[17] & 0xFF) << 16) |
                        ((long)(p[18] & 0xFF) << 8) | (p[19] & 0xFF);
                byte[] data = Arrays.copyOfRange(p, 20, p.length);
                fos.getChannel().write(java.nio.ByteBuffer.wrap(data), offset);
                got = offset + data.length;
                if (got - lastAck > (1 << 20) || got == size) {
                    String a = "{\"id\":\"" + Mlp.jsonEscape(xferId) + "\",\"offset\":" + got + ",\"ok\":true}";
                    Mlp.writeFrame(os, Mlp.TYPE_FILE_ACK, Mlp.utf8(a), sessionKey);
                    lastAck = got;
                }
            }
            byte[] digest = MessageDigest.getInstance("SHA-256").digest(java.nio.file.Files.readAllBytes(dst.toPath()));
            String done = "{\"id\":\"" + Mlp.jsonEscape(xferId) + "\",\"sha256\":\"" + Mlp.hex(digest) + "\"}";
            Mlp.writeFrame(os, Mlp.TYPE_FILE_DONE, Mlp.utf8(done), sessionKey);
            fileCountStatic++;
            setStatus("文件已接收: " + safe);
            sendBroadcast(new Intent(ACTION_STATE));
        } catch (Exception e) {
            Log.e(TAG, "receiveFile", e);
        }
    }

    private void setClipboard(String text) {
        try {
            ClipboardManager cm = (ClipboardManager) getSystemService(Context.CLIPBOARD_SERVICE);
            if (cm != null) cm.setPrimaryClip(ClipData.newPlainText("MoreLink", text));
        } catch (Exception e) {
            Log.e(TAG, "clipboard", e);
        }
    }

    /** Called by NotificationListener to forward a notification to paired PC. */
    public static void forwardNotification(Context c, String app, String title, String text) {
        SharedPreferences p = c.getSharedPreferences(PREFS, Context.MODE_PRIVATE);
        if (!p.getBoolean("paired", false)) return;
        String id = p.getString("id", "");
        String peer = p.getString("peer", "");
        byte[] key = Mlp.deriveKey(p.getString("pin", ""), id, peer);
        String ip = PeerStore.firstWindowsIp(c);
        if (ip == null) return;
        String json = "{\"app\":\"" + Mlp.jsonEscape(app) + "\",\"title\":\"" + Mlp.jsonEscape(title) +
                "\",\"text\":\"" + Mlp.jsonEscape(text) + "\",\"ts\":" + (System.currentTimeMillis()/1000) + "}";
        try (Socket s = new Socket(ip, Mlp.DATA_PORT)) {
            s.setSoTimeout(8000);
            OutputStream os = s.getOutputStream();
            InputStream is = new BufferedInputStream(s.getInputStream());
            byte[] hello = Mlp.helloJson(id, p.getString("name", "Android"), "android", Mlp.DATA_PORT).getBytes(StandardCharsets.UTF_8);
            Mlp.writeFrame(os, Mlp.TYPE_HELLO, hello, null);
            byte[] f = Mlp.readFrame(is, null);
            if (f == null || Mlp.frameType(f) != Mlp.TYPE_HELLO) return;
            Mlp.writeFrame(os, Mlp.TYPE_NOTIFY, Mlp.utf8(json), key);
            notificationCountStatic++;
            statusStatic = "已同步通知: " + title;
            c.sendBroadcast(new Intent(ACTION_STATE));
        } catch (Exception e) {
            Log.d(TAG, "forward notify failed: " + e.getMessage());
        }
    }

    /** Send clipboard text to a peer at ip:port (called from MainActivity). */
    public static String sendClipboard(Context c, String ip, int port, String text) {
        SharedPreferences p = c.getSharedPreferences(PREFS, Context.MODE_PRIVATE);
        if (!p.getBoolean("paired", false)) return "请先配对";
        byte[] key = Mlp.deriveKey(p.getString("pin", ""), p.getString("id", ""), p.getString("peer", ""));
        try (Socket s = new Socket(ip, port)) {
            s.setSoTimeout(8000);
            OutputStream os = s.getOutputStream();
            InputStream is = new BufferedInputStream(s.getInputStream());
            byte[] hello = Mlp.helloJson(p.getString("id", ""), p.getString("name", "Android"),
                    "android", Mlp.DATA_PORT).getBytes(StandardCharsets.UTF_8);
            Mlp.writeFrame(os, Mlp.TYPE_HELLO, hello, null);
            byte[] f = Mlp.readFrame(is, null);
            if (f == null || Mlp.frameType(f) != Mlp.TYPE_HELLO) return "握手失败";
            Mlp.writeFrame(os, Mlp.TYPE_CLIP_TEXT, Mlp.utf8(text), key);
            statusStatic = "剪贴板已发送";
            c.sendBroadcast(new Intent(ACTION_STATE));
            return "已发送";
        } catch (Exception e) {
            return "失败: " + e.getMessage();
        }
    }

    /** Generate a new pairing PIN and display it. */
    public static String newPin(Context c) {
        String pin = Mlp.makePin();
        c.getSharedPreferences(PREFS, MODE_PRIVATE).edit().putString("pin", pin).apply();
        statusStatic = "配对码: " + pin;
        c.sendBroadcast(new Intent(ACTION_STATE));
        return pin;
    }

    @Override
    public void onDestroy() {
        running.set(false);
        stopBeacon.set(true);
        try { if (server != null) server.close(); } catch (Exception ignored) {}
        try { if (discovery != null) discovery.close(); } catch (Exception ignored) {}
        pool.shutdownNow();
        super.onDestroy();
    }

    @Override
    public IBinder onBind(Intent intent) { return null; }
}
