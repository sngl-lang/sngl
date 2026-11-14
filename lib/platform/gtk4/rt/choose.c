/* A calendar's day, set without reporting it, which the platform's SNGL
 * cannot write: the call doing it depends on GTK's version. Compiled into
 * every gtk4 program, which each language compiles beside its own. */
#include "sngl_gtk.h"

/* select_day is the call every GTK 4 has; 4.20 deprecates it for set_date,
 * which the older ones lack. The selection is the program's, not input, so
 * "day-selected" is blocked; a test's invoker fires it itself after
 * selecting. */
void sngl_calendar_select(GtkWidget *w, int y, int m, int d) {
    GtkCalendar *c = GTK_CALENDAR(w);
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
