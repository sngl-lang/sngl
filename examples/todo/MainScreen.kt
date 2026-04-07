package test.sngl.app

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.*
import androidx.compose.material3.pulltorefresh.PullToRefreshBox
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.alpha
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp

data class Todo(
    val text: String = "",
    val done: Boolean = false
)

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun MainScreen() {
    var newTodo by remember { mutableStateOf("") }
    val todos = remember { mutableStateListOf<Todo>() }
    val status by remember { derivedStateOf { (("Todo List (" + todos.size.toString()) + " items)") } }

    Column() {
        Text(
            text = status
        )
        Row() {
            OutlinedTextField(
                onValueChange = { _v_ ->
                    newTodo = _v_
                },
                value = value
            )
            Button(
                onClick = {
                    todos = push(todos, Todo(text = newTodo, done = false))
                    newTodo = ""
                }
            ) {
                Text(
                    onClick = {
                        todos = push(todos, Todo(text = newTodo, done = false))
                        newTodo = ""
                    },
                    text = "Add"
                )
            }
        }
        Column() {
            todos.forEachIndexed { index, item ->
                Row(
                    onCheckedChange = {
                        todos[index].done = !todos[index].done
                    }
                ) {
                    Checkbox(
                        checked = item.done,
                        onCheckedChange = {
                            todos[index].done = !todos[index].done
                        }
                    )
                    if ((item.text != "")) {
                        Text(
                            onCheckedChange = {
                                todos[index].done = !todos[index].done
                            },
                            text = item.text
                        )
                    }
                }
            }
        }
        Button(
            onClick = {
                todos = remove(todos, (todos.size - 1))
            }
        ) {
            Text(
                onClick = {
                    todos = remove(todos, (todos.size - 1))
                },
                text = "Remove"
            )
        }
    }
}
