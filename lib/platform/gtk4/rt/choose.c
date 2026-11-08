/* What a user chooses among on GTK: a calendar's day and a select's
 * drop-down. Compiled into every gtk4 program: pkg/go/gtk4rt includes it for
 * Go, and a C program compiles it beside its own. */
#include "sngl_gtk.h"

#include <stdio.h>
#include <string.h>

/* select_day is the call every GTK 4 has; 4.20 deprecates it for set_date,
 * which the older ones lack. The selection is the program's, not input, so
 * "day-selected" is blocked; a test's invoker fires it itself after
 * selecting. */
void sngl_calendar_select(GtkCalendar *c, int y, int m, int d) {
    GDateTime *dt = g_date_time_new_utc(y, m, d, 0, 0, 0);
    if (dt == NULL) return;
    guint id = g_signal_lookup("day-selected", GTK_TYPE_CALENDAR);
    g_signal_handlers_block_matched(c, G_SIGNAL_MATCH_ID, id, 0, NULL, NULL, NULL);
    G_GNUC_BEGIN_IGNORE_DEPRECATIONS
    gtk_calendar_select_day(c, dt);
    G_GNUC_END_IGNORE_DEPRECATIONS
    g_signal_handlers_unblock_matched(c, G_SIGNAL_MATCH_ID, id, 0, NULL, NULL, NULL);
    g_date_time_unref(dt);
}

void sngl_calendar_select_day(GtkCalendar *c, const char *day) {
    int y, m, d;
    if (sscanf(day, "%d-%d-%d", &y, &m, &d) == 3) sngl_calendar_select(c, y, m, d);
}

void sngl_calendar_date(GtkCalendar *c, char day[11]) {
    GDateTime *dt = gtk_calendar_get_date(c);
    g_snprintf(day, 11, "%04d-%02d-%02d", g_date_time_get_year(dt), g_date_time_get_month(dt), g_date_time_get_day_of_month(dt));
    g_date_time_unref(dt);
}

/* A select's choices are a GtkStringList, since no SNGL expression produces a
 * GListModel, offered as the labels joined with newlines -- so a label
 * holding a newline is offered as two choices. Offering them, or the value
 * bound, is no choice anyone made, so notify::selected is blocked meanwhile;
 * and a model is replaced only for labels that differ, which would otherwise
 * drop the popup a user has open. A drop-down a render has not built yet is
 * offered nothing: a settle the render's own instances ask for runs before
 * it, and the drop-down's build mounts the offer again. */
void sngl_drop_down_offer(GtkDropDown *d, const char *labels, const char *chosen) {
    if (d == NULL) return;
    guint notify = g_signal_lookup("notify", G_TYPE_OBJECT);
    GQuark selected = g_quark_from_static_string("selected");
    g_signal_handlers_block_matched(d, G_SIGNAL_MATCH_ID | G_SIGNAL_MATCH_DETAIL, notify, selected, NULL, NULL, NULL);
    gchar **split = g_strsplit(labels, "\n", -1);
    guint n = g_strv_length(split);
    GListModel *cur = gtk_drop_down_get_model(d);
    gboolean same = cur != NULL && GTK_IS_STRING_LIST(cur) && g_list_model_get_n_items(cur) == n;
    for (guint i = 0; same && i < n; i++) {
        same = strcmp(gtk_string_list_get_string(GTK_STRING_LIST(cur), i), split[i]) == 0;
    }
    if (!same) {
        GtkStringList *list = gtk_string_list_new((const char *const *)split);
        gtk_drop_down_set_model(d, G_LIST_MODEL(list));
        g_object_unref(list);
    }
    guint at = GTK_INVALID_LIST_POSITION;
    for (guint i = 0; i < n; i++) {
        if (strcmp(split[i], chosen) == 0) {
            at = i;
            break;
        }
    }
    gtk_drop_down_set_selected(d, at);
    g_strfreev(split);
    g_signal_handlers_unblock_matched(d, G_SIGNAL_MATCH_ID | G_SIGNAL_MATCH_DETAIL, notify, selected, NULL, NULL, NULL);
}

const char *sngl_drop_down_label(GtkDropDown *d) {
    gpointer item = gtk_drop_down_get_selected_item(d);
    if (item == NULL || !GTK_IS_STRING_OBJECT(item)) return "";
    return gtk_string_object_get_string(GTK_STRING_OBJECT(item));
}

/* The selection's handler may render the page the drop-down is on afresh,
 * which drops the drop-down while GTK is still setting it, so it is held
 * until the selection is done. */
void sngl_drop_down_select(GtkDropDown *d, const char *label) {
    GListModel *m = gtk_drop_down_get_model(d);
    if (m == NULL || !GTK_IS_STRING_LIST(m)) return;
    guint n = g_list_model_get_n_items(m);
    for (guint i = 0; i < n; i++) {
        if (strcmp(gtk_string_list_get_string(GTK_STRING_LIST(m), i), label) == 0) {
            g_object_ref(d);
            gtk_drop_down_set_selected(d, i);
            g_object_unref(d);
            return;
        }
    }
}
