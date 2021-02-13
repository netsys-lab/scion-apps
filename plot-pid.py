#! /bin/python3

import time
import plotly.express as px
import pandas as pd
import dash
import dash_core_components as dcc
import dash_html_components as html
from dash.dependencies import Input, Output

app = dash.Dash()
app.layout = html.Div([
    dcc.Graph(id='live-figure'),
    dcc.Interval(
        id='interval-component',
        interval=500, # in milliseconds
        n_intervals=0
    )
])

@app.callback(Output('live-figure', 'figure'),
              Input('interval-component', 'n_intervals'))
def updage_figure(n):
    df = pd.read_csv("pid.csv")
    fig = px.line(df, y = 'Mibps')

    return fig

app.run_server()
