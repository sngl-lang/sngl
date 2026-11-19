/* What a user chooses among on GTK is gtk4.sngl's, but for the one call
 * here. Compiled into every gtk4 program, which each language compiles
 * beside its own. */
#include "sngl_gtk.h"

/* select_day is the call every GTK 4 has; 4.20 deprecates it for set_date,
 * which the older ones lack. A call written in SNGL cannot say it knows, and
 * every build of a program calling it would warn. */
void sngl_calendar_select_date(GtkWidget *w, GDateTime *day) {
    G_GNUC_BEGIN_IGNORE_DEPRECATIONS
    gtk_calendar_select_day(GTK_CALENDAR(w), day);
    G_GNUC_END_IGNORE_DEPRECATIONS
}
