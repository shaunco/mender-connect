// convert an unsafe pointer to a GDBusConnection structure
static GDBusConnection *to_gdbusconnection(void *ptr)
{
    return (GDBusConnection *)ptr;
}

// convert an unsafe pointer to a GDBusProxy structure
static GDBusProxy *to_gdbusproxy(void *ptr)
{
    return (GDBusProxy *)ptr;
}

// convert an unsafe pointer to a GMainLoop structure
static GMainLoop *to_gmainloop(void *ptr)
{
    return (GMainLoop *)ptr;
}

// convert an unsafe pointer to a GVariant structure
static GVariant *to_gvariant(void *ptr)
{
    return (GVariant *)ptr;
}

// creates a new string from a GVariant
static gchar *string_from_g_variant(GVariant *value)
{
    gchar *str;
    g_variant_get(value, "(s)", &str);
    return str;
}

// creates a new boolean from a GVariant
static gboolean boolean_from_g_variant(GVariant *value)
{
    gboolean b;
    g_variant_get(value, "(b)", &b);
    return b;
}

// exported by golang, see dbus_libgio.go
void handle_on_signal_callback(
    GDBusProxy *proxy,
    gchar *sender_name,
    gchar *signal_name,
    GVariant *parameters,
    gpointer user_data);

// callback registered via g_signal_connect
static void on_signal(
    GDBusProxy *proxy,
    gchar *sender_name,
    gchar *signal_name,
    GVariant *parameters,
    gpointer user_data)
{
    handle_on_signal_callback(
        proxy, sender_name, signal_name, parameters, user_data);
}

// calls g_signal_connect on a proxy instance
static void g_signal_connect_on_proxy(GDBusProxy *proxy)
{
    g_signal_connect(proxy, "g-signal", G_CALLBACK(on_signal), NULL);
}
