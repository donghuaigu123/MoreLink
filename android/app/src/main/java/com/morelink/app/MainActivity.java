package com.morelink.app;

import android.Manifest;
import android.app.Activity;
import android.content.BroadcastReceiver;
import android.content.ClipData;
import android.content.ClipboardManager;
import android.content.Context;
import android.content.Intent;
import android.content.IntentFilter;
import android.content.SharedPreferences;
import android.content.pm.PackageManager;
import android.database.Cursor;
import android.net.Uri;
import android.os.Build;
import android.os.Bundle;
import android.provider.OpenableColumns;
import android.provider.Settings;
import android.text.InputType;
import android.text.method.ScrollingMovementMethod;
import android.util.TypedValue;
import android.view.Gravity;
import android.view.View;
import android.view.ViewGroup;
import android.widget.Button;
import android.widget.EditText;
import android.widget.LinearLayout;
import android.widget.ScrollView;
import android.widget.TextView;
import android.widget.Toast;

import org.json.JSONArray;
import org.json.JSONObject;

import java.io.File;
import java.io.FileOutputStream;
import java.io.InputStream;

/** Main UI: high-contrast dark theme, pairing, peers, clipboard, files, permissions. */
public class MainActivity extends Activity {
    private static final int REQ_PICK_FILE = 42;
    private static final int BG = 0xFF0F1420;
    private static final int PANEL = 0xFF1A2438;
    private static final int ACCENT = 0xFF6EE7B7;
    private static final int ACCENT2 = 0xFF38BDF8;
    private static final int TEXT = 0xFFE6EDF7;
    private static final int MUTED = 0xFF9FB0C8;

    private TextView statusView, peersView, infoView, logView;
    private EditText pinEdit, targetIpEdit, clipEdit;
    private EditText relayHostEdit, relayPortEdit, relayRoomEdit, relayPinEdit;

    private final BroadcastReceiver receiver = new BroadcastReceiver() {
        @Override public void onReceive(Context c, Intent i) { refresh(); }
    };

    @Override
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);
        setTitle("MoreLink · 跨设备协同");
        SharedPreferences p = getSharedPreferences(LinkService.PREFS, MODE_PRIVATE);
        if (p.getString("name", "").isEmpty()) p.edit().putString("name", android.os.Build.MODEL).apply();

        ScrollView scroll = new ScrollView(this);
        scroll.setBackgroundColor(BG);
        LinearLayout root = new LinearLayout(this);
        root.setOrientation(LinearLayout.VERTICAL);
        int pad = dp(16);
        root.setPadding(pad, pad, pad, pad);
        scroll.addView(root, new ScrollView.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT));

        TextView header = new TextView(this);
        header.setText("◆ MoreLink  ·  跨设备协同");
        header.setTextSize(TypedValue.COMPLEX_UNIT_SP, 22);
        header.setTextColor(ACCENT);
        header.setGravity(Gravity.CENTER);
        header.setPadding(0, dp(6), 0, dp(12));
        root.addView(header);

        infoView = mono();
        root.addView(card(infoView, "本机信息"));

        statusView = new TextView(this);
        statusView.setTextSize(TypedValue.COMPLEX_UNIT_SP, 15);
        statusView.setTextColor(ACCENT2);
        statusView.setPadding(dp(12), dp(12), dp(12), dp(12));
        root.addView(panel(statusView));

        root.addView(section("① 配对（两端使用相同 6 位配对码）"));
        pinEdit = edit("配对码（生成或输入电脑端的码）");
        pinEdit.setInputType(InputType.TYPE_CLASS_NUMBER);
        root.addView(panel(pinEdit));
        LinearLayout pairRow = row();
        pairRow.addView(button("生成配对码", v -> {
            String pin = LinkService.newPin(this);
            pinEdit.setText(pin);
            toast("配对码：" + pin + " 请在电脑端输入相同码");
        }));
        pairRow.addView(button("复制", v -> copyText(pinEdit.getText().toString())));
        root.addView(pairRow);
        targetIpEdit = edit("电脑 IP（从下方设备列表点选）");
        root.addView(panel(targetIpEdit));
        root.addView(button("配对到电脑（主动连接）", v -> doPair()));

        root.addView(section("② 附近设备（点击自动填入 IP）"));
        peersView = mono();
        peersView.setTextSize(TypedValue.COMPLEX_UNIT_SP, 13);
        root.addView(card(peersView, ""));

        root.addView(section("③ 剪贴板互传"));
        clipEdit = edit("要发送到电脑的文本");
        clipEdit.setMaxLines(3);
        clipEdit.setSingleLine(false);
        root.addView(panel(clipEdit));
        LinearLayout clipRow = row();
        clipRow.addView(button("发送文本", v -> sendClip()));
        clipRow.addView(button("读取本机剪贴板", v -> {
            String t = readClipboard();
            if (t != null) clipEdit.setText(t); else toast("剪贴板为空");
        }));
        root.addView(clipRow);

        root.addView(section("④ 文件互传"));
        root.addView(button("选择文件发送到电脑", v -> pickFile()));
        TextView fhint = new TextView(this);
        SharedPreferences sp0 = getSharedPreferences(LinkService.PREFS, MODE_PRIVATE);
        String recvDir = getExternalFilesDir(null) + "/Received";
        fhint.setText("接收文件保存在:\n" + recvDir + "\n（可在下方“接收目录”中查看/另存）");
        fhint.setTextColor(MUTED);
        fhint.setTextSize(TypedValue.COMPLEX_UNIT_SP, 12);
        root.addView(fhint);
        root.addView(button("查看接收目录", v -> {
            android.net.Uri uri = android.net.Uri.parse("content://com.morelink.app.files/" + recvDir);
            try {
                Intent i = new Intent(Intent.ACTION_VIEW);
                i.setDataAndType(uri, "resource/folder");
                startActivity(i);
            } catch (Exception e) { toast("接收目录: " + recvDir); }
        }));

        root.addView(section("⑤ 跨网络中继（无公网IP时）"));
        relayHostEdit = edit("中继服务器地址（域名或IP）");
        relayHostEdit.setText(sp0.getString("relay_host", ""));
        root.addView(panel(relayHostEdit));
        relayPortEdit = edit("中继端口（默认 47893）");
        relayPortEdit.setInputType(InputType.TYPE_CLASS_NUMBER);
        int rport = sp0.getInt("relay_port", Mlp.RELAY_PORT);
        relayPortEdit.setText(String.valueOf(rport));
        root.addView(panel(relayPortEdit));
        relayRoomEdit = edit("房间码（两端相同）");
        relayRoomEdit.setText(sp0.getString("relay_room", ""));
        root.addView(panel(relayRoomEdit));
        relayPinEdit = edit("中继配对码（两端相同的6位数字）");
        relayPinEdit.setInputType(InputType.TYPE_CLASS_NUMBER);
        relayPinEdit.setText(sp0.getString("relay_pin", ""));
        root.addView(panel(relayPinEdit));
        LinearLayout relayRow = row();
        relayRow.addView(button("连接中继", v -> {
            String host = relayHostEdit.getText().toString().trim();
            int port;
            try { port = Integer.parseInt(relayPortEdit.getText().toString().trim()); }
            catch (Exception e) { port = Mlp.RELAY_PORT; }
            String room = relayRoomEdit.getText().toString().trim();
            String pin = relayPinEdit.getText().toString().trim();
            if (host.isEmpty() || room.isEmpty() || pin.length() != 6) {
                toast("请填服务器地址、房间码和6位配对码"); return;
            }
            LinkService.setRelayEnabled(this, true, host, port, room, pin);
            startService();
            toast("正在连接中继…");
        }));
        relayRow.addView(button("断开中继", v -> {
            LinkService.setRelayEnabled(this, false, "", Mlp.RELAY_PORT, "", "");
            toast("中继已断开");
        }));
        root.addView(relayRow);

        root.addView(section("⑥ 权限与后台"));
        root.addView(button("开启通知读取权限", v -> requestNotificationAccess()));
        root.addView(button("启动 / 保持后台运行", v -> { startService(); toast("服务已启动"); }));

        root.addView(section("状态日志"));
        logView = mono();
        logView.setMovementMethod(new ScrollingMovementMethod());
        root.addView(card(logView, ""));

        TextView foot = new TextView(this);
        foot.setText("MoreLink v2.0.0 · 原创开源 · 端到端 AES-256-GCM 加密\n局域网自动发现 + 可选中继跨网络");
        foot.setTextColor(MUTED);
        foot.setTextSize(TypedValue.COMPLEX_UNIT_SP, 11);
        foot.setGravity(Gravity.CENTER);
        foot.setPadding(0, dp(18), 0, dp(8));
        root.addView(foot);

        setContentView(scroll);
        registerReceiver(receiver, new IntentFilter(LinkService.ACTION_STATE));
        startService();
        requestPermissions();
        refresh();
    }

    private LinearLayout row() {
        LinearLayout r = new LinearLayout(this);
        r.setOrientation(LinearLayout.HORIZONTAL);
        r.setPadding(0, dp(6), 0, dp(6));
        return r;
    }

    private TextView section(String title) {
        TextView t = new TextView(this);
        t.setText("── " + title + " ──");
        t.setTextSize(TypedValue.COMPLEX_UNIT_SP, 14);
        t.setTextColor(ACCENT);
        t.setGravity(Gravity.CENTER);
        t.setPadding(0, dp(16), 0, dp(8));
        return t;
    }

    private TextView mono() {
        TextView t = new TextView(this);
        t.setTextSize(TypedValue.COMPLEX_UNIT_SP, 13);
        t.setTextColor(TEXT);
        t.setPadding(dp(12), dp(10), dp(12), dp(10));
        return t;
    }

    private EditText edit(String hint) {
        EditText e = new EditText(this);
        e.setHint(hint);
        e.setHintTextColor(MUTED);
        e.setTextColor(TEXT);
        e.setBackgroundColor(PANEL);
        e.setPadding(dp(12), dp(12), dp(12), dp(12));
        return e;
    }

    private Button button(String text, View.OnClickListener l) {
        Button b = new Button(this);
        b.setText(text);
        b.setTextColor(0xFF05231A);
        b.setBackgroundColor(ACCENT);
        b.setAllCaps(false);
        b.setOnClickListener(l);
        LinearLayout.LayoutParams lp = new LinearLayout.LayoutParams(0,
                ViewGroup.LayoutParams.WRAP_CONTENT, 1f);
        lp.setMargins(dp(4), dp(4), dp(4), dp(4));
        b.setLayoutParams(lp);
        return b;
    }

    private View panel(View child) {
        LinearLayout p = new LinearLayout(this);
        p.setOrientation(LinearLayout.VERTICAL);
        p.setBackgroundColor(PANEL);
        p.setPadding(dp(4), dp(4), dp(4), dp(4));
        p.addView(child);
        LinearLayout.LayoutParams lp = new LinearLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT);
        lp.setMargins(0, dp(4), 0, dp(4));
        p.setLayoutParams(lp);
        return p;
    }

    private View card(View child, String title) {
        LinearLayout c = new LinearLayout(this);
        c.setOrientation(LinearLayout.VERTICAL);
        c.setBackgroundColor(PANEL);
        c.setPadding(dp(12), dp(10), dp(12), dp(10));
        if (!title.isEmpty()) {
            TextView h = new TextView(this);
            h.setText(title);
            h.setTextColor(MUTED);
            h.setTextSize(TypedValue.COMPLEX_UNIT_SP, 12);
            c.addView(h);
        }
        c.addView(child);
        LinearLayout.LayoutParams lp = new LinearLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT);
        lp.setMargins(0, dp(4), 0, dp(4));
        c.setLayoutParams(lp);
        return c;
    }

    private void doPair() {
        String ip = targetIpEdit.getText().toString().trim();
        String pin = pinEdit.getText().toString().trim();
        if (ip.isEmpty()) { toast("请填写电脑 IP"); return; }
        if (pin.length() != 6) { toast("请输入 6 位配对码"); return; }
        toast(LinkService.pairWithPeer(this, ip, Mlp.DATA_PORT, pin));
        refresh();
    }

    private void sendClip() {
        String ip = targetIpEdit.getText().toString().trim();
        String text = clipEdit.getText().toString();
        boolean relay = getSharedPreferences(LinkService.PREFS, MODE_PRIVATE).getBoolean("relay_enabled", false);
        if (ip.isEmpty() && !relay) { toast("请填写电脑 IP（或先连接中继）"); return; }
        if (text.isEmpty()) { toast("请输入文本"); return; }
        toast(LinkService.sendClipboard(this, ip, Mlp.DATA_PORT, text));
    }

    private void pickFile() {
        Intent i = new Intent(Intent.ACTION_GET_CONTENT);
        i.setType("*/*");
        i.addCategory(Intent.CATEGORY_OPENABLE);
        try { startActivityForResult(Intent.createChooser(i, "选择要发送的文件"), REQ_PICK_FILE); }
        catch (Exception e) { toast("无法打开选择器"); }
    }

    @Override
    protected void onActivityResult(int req, int res, Intent data) {
        super.onActivityResult(req, res, data);
        if (req == REQ_PICK_FILE && res == RESULT_OK && data != null && data.getData() != null) {
            Uri uri = data.getData();
            final String name = queryName(uri);
            final String ip = targetIpEdit.getText().toString().trim();
            boolean relay = getSharedPreferences(LinkService.PREFS, MODE_PRIVATE).getBoolean("relay_enabled", false);
            if (ip.isEmpty() && !relay) { toast("请先填写电脑 IP（或先连接中继）"); return; }
            new Thread(() -> {
                String path = copyUriToCache(uri, name);
                if (path == null) { runOnUiThread(() -> toast("无法读取文件")); return; }
                final String r = LinkService.sendFile(MainActivity.this, ip, Mlp.DATA_PORT, path, name);
                runOnUiThread(() -> { toast(r); refresh(); });
            }).start();
        }
    }

    private String queryName(Uri uri) {
        String name = "file";
        try (Cursor c = getContentResolver().query(uri, null, null, null, null)) {
            if (c != null && c.moveToFirst()) {
                int idx = c.getColumnIndex(OpenableColumns.DISPLAY_NAME);
                if (idx >= 0) name = c.getString(idx);
            }
        } catch (Exception ignored) {}
        return name;
    }

    private String copyUriToCache(Uri uri, String name) {
        try {
            File dir = new File(getCacheDir(), "out");
            dir.mkdirs();
            File dst = new File(dir, name);
            try (InputStream in = getContentResolver().openInputStream(uri);
                 FileOutputStream out = new FileOutputStream(dst)) {
                byte[] b = new byte[64 << 10];
                int n;
                while ((n = in.read(b)) > 0) out.write(b, 0, n);
            }
            return dst.getAbsolutePath();
        } catch (Exception e) { return null; }
    }

    private String readClipboard() {
        try {
            ClipboardManager cm = (ClipboardManager) getSystemService(Context.CLIPBOARD_SERVICE);
            if (cm != null && cm.hasPrimaryClip() && cm.getPrimaryClip().getItemCount() > 0) {
                CharSequence t = cm.getPrimaryClip().getItemAt(0).getText();
                return t == null ? null : t.toString();
            }
        } catch (Exception ignored) {}
        return null;
    }

    private void copyText(String s) {
        try {
            ClipboardManager cm = (ClipboardManager) getSystemService(Context.CLIPBOARD_SERVICE);
            if (cm != null) cm.setPrimaryClip(ClipData.newPlainText("MoreLink", s));
            toast("已复制");
        } catch (Exception e) { toast("复制失败"); }
    }

    private void startService() {
        Intent i = new Intent(this, LinkService.class);
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) startForegroundService(i);
        else startService(i);
    }

    private void requestPermissions() {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
            if (checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED)
                requestPermissions(new String[]{Manifest.permission.POST_NOTIFICATIONS}, 1);
        }
    }

    private void requestNotificationAccess() {
        try {
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.LOLLIPOP_MR1)
                startActivity(new Intent(Settings.ACTION_NOTIFICATION_LISTENER_SETTINGS));
            else
                startActivity(new Intent("android.settings.ACTION_NOTIFICATION_LISTENER_SETTINGS"));
        } catch (Exception e) { toast("请到 设置→通知使用权 中开启 MoreLink"); }
    }

    private void refresh() {
        if (statusView == null) return;
        String json = LinkService.stateJson(this);
        try {
            JSONObject o = new JSONObject(json);
            statusView.setText("状态: " + o.optString("status") + "\n已配对: " +
                    (o.optBoolean("paired") ? "是" : "否") + " · 通知: " + o.optInt("notifications") +
                    " · 文件: " + o.optInt("files"));
        } catch (Exception ignored) {}

        SharedPreferences p = getSharedPreferences(LinkService.PREFS, MODE_PRIVATE);
        infoView.setText("设备名: " + p.getString("name", "") + "\nID: " + p.getString("id", "") +
                "\n配对码: " + p.getString("pin", ""));

        String peers = PeerStore.peersJson(this);
        StringBuilder sb = new StringBuilder();
        int count = 0;
        try {
            JSONArray arr = new JSONArray(peers);
            for (int i = 0; i < arr.length(); i++) {
                JSONObject o = arr.getJSONObject(i);
                String ip = o.optString("ip");
                if (ip.isEmpty()) continue;
                sb.append("• ").append(o.optString("name")).append(" [").append(o.optString("platform"))
                  .append("]  ").append(ip).append("\n");
                count++;
            }
        } catch (Exception ignored) {}
        peersView.setText(sb.length() == 0 ? "暂未发现设备（确保两端在同一局域网且电脑端已启动）" : sb.toString());

        if (count == 1 && targetIpEdit.getText().toString().isEmpty()) {
            try {
                JSONArray arr = new JSONArray(peers);
                for (int i = 0; i < arr.length(); i++) {
                    String ip = arr.getJSONObject(i).optString("ip");
                    if (!ip.isEmpty()) { targetIpEdit.setText(ip); break; }
                }
            } catch (Exception ignored) {}
        }
        logView.setText("提示：\n• 首次使用先在电脑端点“生成”获得配对码\n• 手机输入相同配对码并填电脑 IP，点“配对到电脑”\n• 配对成功后即可互传剪贴板、文件、同步通知");
    }

    private void toast(String s) { Toast.makeText(this, s, Toast.LENGTH_SHORT).show(); }
    private int dp(int v) {
        return (int) TypedValue.applyDimension(TypedValue.COMPLEX_UNIT_DIP, v, getResources().getDisplayMetrics());
    }

    @Override
    protected void onDestroy() {
        try { unregisterReceiver(receiver); } catch (Exception ignored) {}
        super.onDestroy();
    }
}
