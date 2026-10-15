package PACKAGE

import androidx.compose.material3.DatePicker
import androidx.compose.material3.DatePickerDialog
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.rememberDatePickerState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import java.time.Instant
import java.time.LocalDate
import java.time.ZoneOffset
import java.time.format.DateTimeParseException

// SnglDateField is sngl:ui's datepicker: a field a date is typed into, as the
// "YYYY-MM-DD" a SNGL date is on this target, with a button beside it opening
// Material's date picker. What is typed is reported once it is a date, and
// clearing the field reports none; the dialog's OK reports the day picked and
// Cancel nothing.
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun SnglDateField(
    value: String?,
    label: String,
    enabled: Boolean,
    modifier: Modifier,
    onValueChange: (String?) -> Unit,
) {
    // The text is the field's own while it is half a date, and starts again
    // from the value whenever the value changes from outside.
    var text by remember(value) { mutableStateOf(value ?: "") }
    var open by remember { mutableStateOf(false) }
    OutlinedTextField(
        value = text,
        onValueChange = { typed ->
            text = typed
            if (typed.isEmpty()) {
                onValueChange(null)
            } else {
                parseDate(typed)?.let { onValueChange(it) }
            }
        },
        enabled = enabled,
        singleLine = true,
        label = if (label.isEmpty()) null else ({ Text(label) }),
        trailingIcon = {
            TextButton(onClick = { open = true }, enabled = enabled) { Text("Pick") }
        },
        modifier = modifier,
    )
    if (open) {
        val state = rememberDatePickerState(
            initialSelectedDateMillis = (value?.let { parseDate(it) })?.let {
                LocalDate.parse(it).atStartOfDay(ZoneOffset.UTC).toInstant().toEpochMilli()
            },
        )
        DatePickerDialog(
            onDismissRequest = { open = false },
            confirmButton = {
                TextButton(onClick = {
                    open = false
                    state.selectedDateMillis?.let {
                        onValueChange(Instant.ofEpochMilli(it).atZone(ZoneOffset.UTC).toLocalDate().toString())
                    }
                }) { Text("OK") }
            },
            dismissButton = { TextButton(onClick = { open = false }) { Text("Cancel") } },
        ) {
            DatePicker(state = state)
        }
    }
}

// The date s is in "YYYY-MM-DD", or null when it is not one.
private fun parseDate(s: String): String? =
    try {
        LocalDate.parse(s).toString()
    } catch (e: DateTimeParseException) {
        null
    }
