"""TCP endpoint of the visualizer's panel protocol, painting into a hub75 display."""

import esphome.codegen as cg
from esphome.components import socket
from esphome.components.hub75.display import HUB75Display
import esphome.config_validation as cv
from esphome.const import CONF_ID, CONF_PORT

DEPENDENCIES = ["network", "display"]

CONF_DISPLAY_ID = "display_id"
CONF_MAX_BRIGHTNESS = "max_brightness"

panel_stream_ns = cg.esphome_ns.namespace("panel_stream")
PanelStream = panel_stream_ns.class_("PanelStream", cg.Component)


def _sockets(config):
    # one listener and one client; lwIP's socket pool is sized from these counts
    socket.consume_sockets(1, "panel_stream", socket.SocketType.TCP_LISTEN)(config)
    socket.consume_sockets(1, "panel_stream")(config)
    return config


CONFIG_SCHEMA = cv.All(
    cv.Schema(
        {
            cv.GenerateID(): cv.declare_id(PanelStream),
            cv.Required(CONF_DISPLAY_ID): cv.use_id(HUB75Display),
            cv.Optional(CONF_PORT, default=7090): cv.port,
            cv.Optional(CONF_MAX_BRIGHTNESS, default=255): cv.int_range(min=1, max=255),
        }
    ).extend(cv.COMPONENT_SCHEMA),
    _sockets,
)


async def to_code(config):
    var = cg.new_Pvariable(config[CONF_ID])
    await cg.register_component(var, config)
    cg.add(var.set_display(await cg.get_variable(config[CONF_DISPLAY_ID])))
    cg.add(var.set_port(config[CONF_PORT]))
    cg.add(var.set_max_brightness(config[CONF_MAX_BRIGHTNESS]))
